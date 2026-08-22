package authctrl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	authv1 "github.com/maintainerd/core/gen/maintainerd/auth/v1"
	"github.com/maintainerd/core/internal/steward"
)

// listPageSize bounds every lookup. The control catalog is small (a handful of
// objects per service), so one page is always enough; a page is requested
// explicitly rather than relying on whatever auth's default happens to be.
const listPageSize = 200

// Status is what happened to one catalog object during a pass.
type Status string

const (
	// StatusCreated means this run wrote the object into auth.
	StatusCreated Status = "created"
	// StatusEnsured means the object already matched — no write was made. A
	// second run of an unchanged catalog reports nothing but this.
	StatusEnsured Status = "ensured"
	// StatusFailed means this object could not be converged. The pass continues:
	// one unreachable object should not strand the rest of the catalog.
	StatusFailed Status = "failed"
)

// Outcome is the per-object record of a pass.
type Outcome struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Report is one reconcile pass, suitable for logging and for the REST surface.
type Report struct {
	Totals    steward.Report `json:"totals"`
	Outcomes  []Outcome      `json:"outcomes"`
	Created   int            `json:"created"`
	Ensured   int            `json:"ensured"`
	Failed    int            `json:"failed"`
	Transport string         `json:"transport"`
	StartedAt time.Time      `json:"started_at"`
	Duration  string         `json:"duration"`
}

// TransportControlClient names the post-setup path in a Report, distinguishing
// it from work done during the setup window (TransportSetupWindow).
const (
	TransportControlClient = "control-client"
	TransportSetupWindow   = "setup-window"
)

// ---- narrow client seams ----------------------------------------------------
//
// Each is the exact slice of one auth service this applier uses. The generated
// clients satisfy them structurally, and a test can supply a fake without
// standing up a gRPC server. Anything absent from these interfaces is something
// this package has decided not to do — see the package doc's boundary.

type serviceClient interface {
	ListServices(context.Context, *authv1.ListServicesRequest, ...grpc.CallOption) (*authv1.ListServicesResponse, error)
	CreateService(context.Context, *authv1.CreateServiceRequest, ...grpc.CallOption) (*authv1.CreateServiceResponse, error)
	AssignServicePolicy(context.Context, *authv1.AssignServicePolicyRequest, ...grpc.CallOption) (*authv1.AssignServicePolicyResponse, error)
}

type apiClient interface {
	ListAPIs(context.Context, *authv1.ListAPIsRequest, ...grpc.CallOption) (*authv1.ListAPIsResponse, error)
	CreateAPI(context.Context, *authv1.CreateAPIRequest, ...grpc.CallOption) (*authv1.CreateAPIResponse, error)
}

type permissionClient interface {
	ListPermissions(context.Context, *authv1.ListPermissionsRequest, ...grpc.CallOption) (*authv1.ListPermissionsResponse, error)
	CreatePermission(context.Context, *authv1.CreatePermissionRequest, ...grpc.CallOption) (*authv1.CreatePermissionResponse, error)
}

type policyClient interface {
	ListPolicies(context.Context, *authv1.ListPoliciesRequest, ...grpc.CallOption) (*authv1.ListPoliciesResponse, error)
	CreatePolicy(context.Context, *authv1.CreatePolicyRequest, ...grpc.CallOption) (*authv1.CreatePolicyResponse, error)
}

type oauthClientClient interface {
	ListClients(context.Context, *authv1.ListClientsRequest, ...grpc.CallOption) (*authv1.ListClientsResponse, error)
	CreateClient(context.Context, *authv1.CreateClientRequest, ...grpc.CallOption) (*authv1.CreateClientResponse, error)
}

// KeyStore is the read+write half of service-client key custody the applier
// needs. *steward.FileKeyStore satisfies it, and it is the SAME store the
// setup-window applier writes through.
type KeyStore interface {
	PrivateKey(service string) (string, bool, error)
	Record(ctx context.Context, service string, privateKeyPEM string) error
	Discard(service string) error
}

// Registry converges core's OWN services table after an auth-side object
// applies. Without it a capability's registry row is inserted once at setup and
// left at status 'pending' forever, even though the service is fully wired.
// *service.Service satisfies it.
type Registry interface {
	EnsureRegistered(ctx context.Context, name, kind string, isSystem bool) error
}

// Applier converges catalog objects through auth's regular, permission-verified
// management RPCs. It implements steward.Applier.
//
// Every method is get-or-create: look the object up by name, create it only if
// absent, and never rewrite or delete. Per-object failures are recorded and
// swallowed so one bad object does not strand the rest of the catalog — read
// Report(), not the error return, to find out what actually happened.
type Applier struct {
	tenantID string

	services    serviceClient
	apis        apiClient
	permissions permissionClient
	policies    policyClient
	clients     oauthClientClient

	keys     KeyStore
	registry Registry

	// Resolved-so-far state. Catalog order guarantees a Service is applied before
	// the ResourceAPI/ServiceClient/ServicePolicy that reference it, so an object
	// whose dependency is absent here failed rather than merely arrived early.
	serviceIDs map[string]string

	outcomes []Outcome
}

// applierDeps is the set of seams an Applier drives. Kept unexported: the
// production constructor takes a connected Client, and tests in this package
// supply fakes directly.
type applierDeps struct {
	tenantID    string
	services    serviceClient
	apis        apiClient
	permissions permissionClient
	policies    policyClient
	clients     oauthClientClient
	keys        KeyStore
	registry    Registry
}

// NewApplier builds the regular-surface applier over a connected Client.
func NewApplier(c *Client, keys KeyStore, registry Registry) *Applier {
	return newApplier(applierDeps{
		tenantID:    c.TenantID(),
		services:    c.Services,
		apis:        c.APIs,
		permissions: c.Permissions,
		policies:    c.Policies,
		clients:     c.Clients,
		keys:        keys,
		registry:    registry,
	})
}

func newApplier(d applierDeps) *Applier {
	return &Applier{
		tenantID:    d.tenantID,
		services:    d.services,
		apis:        d.apis,
		permissions: d.permissions,
		policies:    d.policies,
		clients:     d.clients,
		keys:        d.keys,
		registry:    d.registry,
		serviceIDs:  map[string]string{},
	}
}

// Outcomes returns the per-object record of the pass so far.
func (a *Applier) Outcomes() []Outcome { return a.outcomes }

func (a *Applier) record(kind, name string, status Status, err error) {
	out := Outcome{Kind: kind, Name: name, Status: status}
	if err != nil {
		out.Error = err.Error()
	}
	a.outcomes = append(a.outcomes, out)
}

// fail records a per-object failure and returns nil so steward.Reconcile keeps
// going. The failure is not lost — it is in the Report, which is what the boot
// loop logs and the REST surface returns.
func (a *Applier) fail(kind, name string, err error) error {
	a.record(kind, name, StatusFailed, err)
	return nil
}

func pageOf(limit int32) *authv1.Pagination {
	return &authv1.Pagination{Page: 1, Limit: limit}
}

// ---- Service ----------------------------------------------------------------

func (a *Applier) EnsureService(ctx context.Context, name string, spec steward.ServiceSpec) error {
	existing, err := a.findService(ctx, name)
	if err != nil {
		return a.fail(string(steward.KindService), name, err)
	}
	status := StatusEnsured
	if existing == nil {
		resp, cerr := a.services.CreateService(ctx, &authv1.CreateServiceRequest{
			TenantId:    a.tenantID,
			Name:        name,
			DisplayName: firstNonEmpty(spec.DisplayName, name),
			Description: spec.Description,
			Version:     firstNonEmpty(spec.Version, "v1"),
			Status:      statusActive,
		})
		if cerr != nil {
			return a.fail(string(steward.KindService), name, cerr)
		}
		existing = resp.GetService()
		status = StatusCreated
	}
	a.serviceIDs[name] = existing.GetServiceId()

	// Registry convergence: the auth-side principal now exists, so core's own row
	// for this capability may move off 'pending'.
	if rerr := a.converge(ctx, name, spec); rerr != nil {
		return a.fail(string(steward.KindService), name, rerr)
	}
	a.record(string(steward.KindService), name, status, nil)
	return nil
}

// converge mirrors an applied capability into core's own service registry.
// A spec with no RegistryKind opts out (see steward.ServiceSpec).
func (a *Applier) converge(ctx context.Context, name string, spec steward.ServiceSpec) error {
	if a.registry == nil || strings.TrimSpace(spec.RegistryKind) == "" {
		return nil
	}
	return a.registry.EnsureRegistered(ctx, name, spec.RegistryKind, spec.Tier.IsSystem())
}

func (a *Applier) findService(ctx context.Context, name string) (*authv1.Service, error) {
	resp, err := a.services.ListServices(ctx, &authv1.ListServicesRequest{
		TenantId: a.tenantID, Name: name, Pagination: pageOf(listPageSize),
	})
	if err != nil {
		return nil, err
	}
	// Auth's name filter may be a partial match; the catalog's identity is the
	// exact name, so the decision to create is made here, not upstream.
	for _, svc := range resp.GetServices() {
		if svc.GetName() == name {
			return svc, nil
		}
	}
	return nil, nil
}

// ---- ResourceAPI ------------------------------------------------------------

func (a *Applier) EnsureResourceAPI(ctx context.Context, name string, spec steward.ResourceAPISpec) error {
	serviceID, ok := a.serviceIDs[spec.Service]
	if !ok {
		return a.fail(string(steward.KindResourceAPI), name,
			fmt.Errorf("service %q has not been applied — it must precede its ResourceAPI in the catalog", spec.Service))
	}

	api, err := a.findAPI(ctx, name)
	if err != nil {
		return a.fail(string(steward.KindResourceAPI), name, err)
	}
	status := StatusEnsured
	if api == nil {
		// NOTE: spec.Identifier (the audience) is NOT settable here. Auth's regular
		// CreateAPI mints the identifier itself; only the setup-window
		// EnsureResourceAPI accepted a caller-supplied one. An API provisioned
		// through this path therefore carries auth's generated identifier, and the
		// catalog's Identifier stays the intent for setup-window provisioning.
		// Changing that needs a field on auth's CreateAPIRequest, not a workaround
		// here.
		resp, cerr := a.apis.CreateAPI(ctx, &authv1.CreateAPIRequest{
			TenantId:    a.tenantID,
			Name:        name,
			DisplayName: firstNonEmpty(spec.DisplayName, name),
			Description: "Resource API for " + spec.Service + ", provisioned from the control catalog.",
			Status:      statusActive,
			ServiceId:   serviceID,
		})
		if cerr != nil {
			return a.fail(string(steward.KindResourceAPI), name, cerr)
		}
		api = resp.GetApi()
		status = StatusCreated
	}

	created, perr := a.ensurePermissions(ctx, api.GetApiId(), spec.Permissions)
	if perr != nil {
		return a.fail(string(steward.KindResourceAPI), name, perr)
	}
	if created > 0 {
		status = StatusCreated
	}
	a.record(string(steward.KindResourceAPI), name, status, nil)
	return nil
}

func (a *Applier) findAPI(ctx context.Context, name string) (*authv1.API, error) {
	resp, err := a.apis.ListAPIs(ctx, &authv1.ListAPIsRequest{
		TenantId: a.tenantID, Name: name, Pagination: pageOf(listPageSize),
	})
	if err != nil {
		return nil, err
	}
	for _, api := range resp.GetApis() {
		if api.GetName() == name {
			return api, nil
		}
	}
	return nil, nil
}

// ensurePermissions adds any catalog permission the API does not already define.
// It reads the existing set ONCE, so a converged API costs a single RPC.
func (a *Applier) ensurePermissions(ctx context.Context, apiID string, want []steward.Permission) (int, error) {
	if len(want) == 0 {
		return 0, nil
	}
	resp, err := a.permissions.ListPermissions(ctx, &authv1.ListPermissionsRequest{
		TenantId: a.tenantID, ApiId: apiID, Pagination: pageOf(listPageSize),
	})
	if err != nil {
		return 0, err
	}
	have := make(map[string]struct{}, len(resp.GetPermissions()))
	for _, p := range resp.GetPermissions() {
		have[p.GetName()] = struct{}{}
	}
	created := 0
	for _, p := range want {
		if _, ok := have[p.Name]; ok {
			continue
		}
		if _, cerr := a.permissions.CreatePermission(ctx, &authv1.CreatePermissionRequest{
			TenantId:    a.tenantID,
			Name:        p.Name,
			Description: p.Description,
			Status:      statusActive,
			ApiId:       apiID,
		}); cerr != nil {
			return created, fmt.Errorf("create permission %q: %w", p.Name, cerr)
		}
		created++
	}
	return created, nil
}

// ---- ServiceClient ----------------------------------------------------------

func (a *Applier) EnsureServiceClient(ctx context.Context, name string, spec steward.ServiceClientSpec) (*steward.GeneratedClient, error) {
	serviceID, ok := a.serviceIDs[spec.Service]
	if !ok {
		return nil, a.fail(string(steward.KindServiceClient), name,
			fmt.Errorf("service %q has not been applied — it must precede its ServiceClient in the catalog", spec.Service))
	}

	existing, err := a.findClient(ctx, name)
	if err != nil {
		return nil, a.fail(string(steward.KindServiceClient), name, err)
	}

	privatePEM, hadKey, err := a.keys.PrivateKey(spec.Service)
	if err != nil {
		return nil, a.fail(string(steward.KindServiceClient), name, err)
	}

	if existing != nil {
		// The client is already registered against a JWKS. If we hold no private
		// key, we cannot prove we own that JWKS — and minting one now would only
		// register nothing and leave a key that authenticates nobody. Surface it.
		if !hadKey {
			return nil, a.fail(string(steward.KindServiceClient), name,
				fmt.Errorf("auth client %q already exists but no local private key was found for service %q", name, spec.Service))
		}
		a.record(string(steward.KindServiceClient), name, StatusEnsured, nil)
		// OAuthClientID is deliberately not recovered here: nothing consumes it on
		// the already-exists path, and the RPC that returns it is a credential
		// read.
		return &steward.GeneratedClient{ClientID: existing.GetClientId(), AlreadyExisted: true}, nil
	}

	generated := false
	if privatePEM == "" {
		privatePEM, _, err = steward.GenerateClientKey()
		if err != nil {
			return nil, a.fail(string(steward.KindServiceClient), name, err)
		}
		// Recorded BEFORE registration: a key that reached auth but was never
		// persisted is unrecoverable, whereas a persisted key that never reached
		// auth is simply re-registered on the next pass.
		if rerr := a.keys.Record(ctx, spec.Service, privatePEM); rerr != nil {
			return nil, a.fail(string(steward.KindServiceClient), name, rerr)
		}
		generated = true
	}

	jwks, err := steward.JWKSFromPrivatePEM(privatePEM)
	if err != nil {
		return nil, a.fail(string(steward.KindServiceClient), name, err)
	}
	cfg, err := a.clientConfig(jwks, spec)
	if err != nil {
		return nil, a.fail(string(steward.KindServiceClient), name, err)
	}

	resp, err := a.clients.CreateClient(ctx, &authv1.CreateClientRequest{
		TenantId:    a.tenantID,
		Name:        name,
		DisplayName: name,
		ClientType:  clientTypeM2M,
		Config:      cfg,
		Status:      statusActive,
		// service_id is what makes this client's tokens carry the `svc` claim the
		// policy bundle and the gRPC authorizer resolve.
		ServiceId: &serviceID,
		// identity_provider_id is deliberately omitted so auth defaults it to the
		// built-in "maintainerd" provider. Naming one here would bind a machine
		// credential to a human login source.
	})
	if err != nil {
		if generated {
			// Roll the orphan back, or the next pass would believe a matching key
			// already exists for a client auth never accepted.
			_ = a.keys.Discard(spec.Service)
		}
		return nil, a.fail(string(steward.KindServiceClient), name, err)
	}

	a.record(string(steward.KindServiceClient), name, StatusCreated, nil)
	out := &steward.GeneratedClient{
		ClientID:      resp.GetClient().GetClientId(),
		OAuthClientID: resp.GetCredentials().GetOauthClientId(),
	}
	if generated {
		out.PrivateKeyPEM = privatePEM
	}
	return out, nil
}

// clientConfig builds the `config` blob auth maps onto the client's columns.
// Machine-credential settings travel in config by design (see the note on
// CreateClientRequest): dedicated fields would be a second way to set the same
// thing, and two sources of truth for a verification key is how the wrong key
// ends up deciding who may authenticate.
func (a *Applier) clientConfig(jwks string, spec steward.ServiceClientSpec) (*structpb.Struct, error) {
	var keySet map[string]any
	if err := json.Unmarshal([]byte(jwks), &keySet); err != nil {
		return nil, fmt.Errorf("decode generated jwks: %w", err)
	}
	scopes := spec.AllowedScopes
	if len(scopes) == 0 {
		// An empty allowlist reads as "every scope" — unbounded for a credential
		// with no user in the loop. Fall back to the service's own audience so the
		// credential is at least bounded to the API it fronts.
		scopes = []string{spec.Audience}
	}
	return structpb.NewStruct(map[string]any{
		"token_endpoint_auth_method": tokenAuthPrivateKeyJWT,
		"grant_types":                toAnySlice([]string{"client_credentials"}),
		"allowed_scopes":             toAnySlice(scopes),
		"jwks":                       keySet,
	})
}

func (a *Applier) findClient(ctx context.Context, name string) (*authv1.Client, error) {
	resp, err := a.clients.ListClients(ctx, &authv1.ListClientsRequest{
		TenantId: a.tenantID, Name: name, Pagination: pageOf(listPageSize),
	})
	if err != nil {
		return nil, err
	}
	for _, c := range resp.GetClients() {
		if c.GetName() == name {
			return c, nil
		}
	}
	return nil, nil
}

// ---- ServicePolicy ----------------------------------------------------------

func (a *Applier) EnsureServicePolicy(ctx context.Context, name string, spec steward.ServicePolicySpec) error {
	if len(spec.AllowedActions) == 0 {
		// A service with no outbound calls needs no policy. Creating an empty one
		// would attach a grant that means nothing and invite it being "fixed" by
		// widening it later.
		a.record(string(steward.KindServicePolicy), name, StatusEnsured, nil)
		return nil
	}
	serviceID, ok := a.serviceIDs[spec.Service]
	if !ok {
		return a.fail(string(steward.KindServicePolicy), name,
			fmt.Errorf("service %q has not been applied — it must precede its ServicePolicy in the catalog", spec.Service))
	}

	policyName := firstNonEmpty(spec.PolicyName, name)
	policy, err := a.findPolicy(ctx, policyName)
	if err != nil {
		return a.fail(string(steward.KindServicePolicy), name, err)
	}
	status := StatusEnsured
	if policy == nil {
		document, derr := policyDocument(spec.AllowedActions)
		if derr != nil {
			return a.fail(string(steward.KindServicePolicy), name, derr)
		}
		description := "Service-to-service policy for " + spec.Service + ", provisioned from the control catalog."
		resp, cerr := a.policies.CreatePolicy(ctx, &authv1.CreatePolicyRequest{
			TenantId:    a.tenantID,
			Name:        policyName,
			Description: &description,
			Document:    document,
			Version:     policyVersion,
			Status:      statusActive,
		})
		if cerr != nil {
			return a.fail(string(steward.KindServicePolicy), name, cerr)
		}
		policy = resp.GetPolicy()
		status = StatusCreated
	}

	// Attachment is idempotent on auth's side, so it runs on both paths: a policy
	// that exists but was never attached (a pass that died in between) heals here.
	if _, aerr := a.services.AssignServicePolicy(ctx, &authv1.AssignServicePolicyRequest{
		TenantId:  a.tenantID,
		ServiceId: serviceID,
		PolicyId:  policy.GetPolicyId(),
	}); aerr != nil {
		return a.fail(string(steward.KindServicePolicy), name, aerr)
	}
	a.record(string(steward.KindServicePolicy), name, status, nil)
	return nil
}

func (a *Applier) findPolicy(ctx context.Context, name string) (*authv1.Policy, error) {
	resp, err := a.policies.ListPolicies(ctx, &authv1.ListPoliciesRequest{
		TenantId: a.tenantID, Name: name, Pagination: pageOf(listPageSize),
	})
	if err != nil {
		return nil, err
	}
	for _, p := range resp.GetPolicies() {
		if p.GetName() == name {
			return p, nil
		}
	}
	return nil, nil
}

// policyDocument renders the catalog's allowed actions as auth's policy
// document: one allow statement enumerating the grants. A bare "*" never
// reaches here — steward.Reconcile refuses it.
func policyDocument(actions []string) (*structpb.Struct, error) {
	return structpb.NewStruct(map[string]any{
		"version": policyVersion,
		"statement": []any{
			map[string]any{
				"effect":   "allow",
				"action":   toAnySlice(actions),
				"resource": toAnySlice([]string{"*"}),
			},
		},
	})
}

func toAnySlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// Auth-side vocabulary, named once so a typo is a compile-time constant and not
// a silently ignored config key.
const (
	statusActive           = "active"
	clientTypeM2M          = "m2m"
	tokenAuthPrivateKeyJWT = "private_key_jwt"
	policyVersion          = "v1"
)
