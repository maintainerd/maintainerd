package authctrl

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authv1 "github.com/maintainerd/core/gen/maintainerd/auth/v1"
	"github.com/maintainerd/core/internal/steward"
)

// fakeAuth is an in-memory stand-in for auth's regular management surface. It
// stores what was created so a second pass can find it, which is exactly the
// get-or-create property under test.
type fakeAuth struct {
	services    map[string]*authv1.Service
	apis        map[string]*authv1.API
	permissions map[string][]*authv1.Permission // api id -> permissions
	policies    map[string]*authv1.Policy
	clients     map[string]*authv1.Client

	createdServices    []*authv1.CreateServiceRequest
	createdAPIs        []*authv1.CreateAPIRequest
	createdPermissions []*authv1.CreatePermissionRequest
	createdPolicies    []*authv1.CreatePolicyRequest
	createdClients     []*authv1.CreateClientRequest
	assignments        []*authv1.AssignServicePolicyRequest

	// failOn makes one RPC fail, keyed by "<rpc>:<name>".
	failOn map[string]error

	nextID int
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{
		services:    map[string]*authv1.Service{},
		apis:        map[string]*authv1.API{},
		permissions: map[string][]*authv1.Permission{},
		policies:    map[string]*authv1.Policy{},
		clients:     map[string]*authv1.Client{},
		failOn:      map[string]error{},
	}
}

func (f *fakeAuth) id(prefix string) string {
	f.nextID++
	return prefix + "-" + string(rune('a'+f.nextID-1))
}

func (f *fakeAuth) check(rpc, name string) error {
	if err, ok := f.failOn[rpc+":"+name]; ok {
		return err
	}
	return nil
}

func (f *fakeAuth) ListServices(_ context.Context, req *authv1.ListServicesRequest, _ ...grpc.CallOption) (*authv1.ListServicesResponse, error) {
	if err := f.check("ListServices", req.GetName()); err != nil {
		return nil, err
	}
	out := &authv1.ListServicesResponse{}
	if svc, ok := f.services[req.GetName()]; ok {
		out.Services = append(out.Services, svc)
	}
	return out, nil
}

func (f *fakeAuth) CreateService(_ context.Context, req *authv1.CreateServiceRequest, _ ...grpc.CallOption) (*authv1.CreateServiceResponse, error) {
	if err := f.check("CreateService", req.GetName()); err != nil {
		return nil, err
	}
	f.createdServices = append(f.createdServices, req)
	svc := &authv1.Service{ServiceId: f.id("svc"), Name: req.GetName(), DisplayName: req.GetDisplayName()}
	f.services[req.GetName()] = svc
	return &authv1.CreateServiceResponse{Service: svc}, nil
}

func (f *fakeAuth) AssignServicePolicy(_ context.Context, req *authv1.AssignServicePolicyRequest, _ ...grpc.CallOption) (*authv1.AssignServicePolicyResponse, error) {
	if err := f.check("AssignServicePolicy", req.GetServiceId()); err != nil {
		return nil, err
	}
	f.assignments = append(f.assignments, req)
	return &authv1.AssignServicePolicyResponse{Assigned: true}, nil
}

func (f *fakeAuth) ListAPIs(_ context.Context, req *authv1.ListAPIsRequest, _ ...grpc.CallOption) (*authv1.ListAPIsResponse, error) {
	if err := f.check("ListAPIs", req.GetName()); err != nil {
		return nil, err
	}
	out := &authv1.ListAPIsResponse{}
	if api, ok := f.apis[req.GetName()]; ok {
		out.Apis = append(out.Apis, api)
	}
	return out, nil
}

func (f *fakeAuth) CreateAPI(_ context.Context, req *authv1.CreateAPIRequest, _ ...grpc.CallOption) (*authv1.CreateAPIResponse, error) {
	if err := f.check("CreateAPI", req.GetName()); err != nil {
		return nil, err
	}
	f.createdAPIs = append(f.createdAPIs, req)
	api := &authv1.API{ApiId: f.id("api"), Name: req.GetName(), Identifier: "api-generated"}
	f.apis[req.GetName()] = api
	return &authv1.CreateAPIResponse{Api: api}, nil
}

func (f *fakeAuth) ListPermissions(_ context.Context, req *authv1.ListPermissionsRequest, _ ...grpc.CallOption) (*authv1.ListPermissionsResponse, error) {
	if err := f.check("ListPermissions", req.GetApiId()); err != nil {
		return nil, err
	}
	return &authv1.ListPermissionsResponse{Permissions: f.permissions[req.GetApiId()]}, nil
}

func (f *fakeAuth) CreatePermission(_ context.Context, req *authv1.CreatePermissionRequest, _ ...grpc.CallOption) (*authv1.CreatePermissionResponse, error) {
	if err := f.check("CreatePermission", req.GetName()); err != nil {
		return nil, err
	}
	f.createdPermissions = append(f.createdPermissions, req)
	perm := &authv1.Permission{PermissionId: f.id("perm"), Name: req.GetName()}
	f.permissions[req.GetApiId()] = append(f.permissions[req.GetApiId()], perm)
	return &authv1.CreatePermissionResponse{Permission: perm}, nil
}

func (f *fakeAuth) ListPolicies(_ context.Context, req *authv1.ListPoliciesRequest, _ ...grpc.CallOption) (*authv1.ListPoliciesResponse, error) {
	if err := f.check("ListPolicies", req.GetName()); err != nil {
		return nil, err
	}
	out := &authv1.ListPoliciesResponse{}
	if p, ok := f.policies[req.GetName()]; ok {
		out.Policies = append(out.Policies, p)
	}
	return out, nil
}

func (f *fakeAuth) CreatePolicy(_ context.Context, req *authv1.CreatePolicyRequest, _ ...grpc.CallOption) (*authv1.CreatePolicyResponse, error) {
	if err := f.check("CreatePolicy", req.GetName()); err != nil {
		return nil, err
	}
	f.createdPolicies = append(f.createdPolicies, req)
	p := &authv1.Policy{PolicyId: f.id("pol"), Name: req.GetName(), Document: req.GetDocument()}
	f.policies[req.GetName()] = p
	return &authv1.CreatePolicyResponse{Policy: p}, nil
}

func (f *fakeAuth) ListClients(_ context.Context, req *authv1.ListClientsRequest, _ ...grpc.CallOption) (*authv1.ListClientsResponse, error) {
	if err := f.check("ListClients", req.GetName()); err != nil {
		return nil, err
	}
	out := &authv1.ListClientsResponse{}
	if c, ok := f.clients[req.GetName()]; ok {
		out.Clients = append(out.Clients, c)
	}
	return out, nil
}

func (f *fakeAuth) CreateClient(_ context.Context, req *authv1.CreateClientRequest, _ ...grpc.CallOption) (*authv1.CreateClientResponse, error) {
	if err := f.check("CreateClient", req.GetName()); err != nil {
		return nil, err
	}
	f.createdClients = append(f.createdClients, req)
	c := &authv1.Client{ClientId: f.id("cli"), Name: req.GetName(), ServiceId: req.ServiceId}
	f.clients[req.GetName()] = c
	return &authv1.CreateClientResponse{
		Client:      c,
		Credentials: &authv1.ClientCredentials{ClientId: c.GetClientId(), OauthClientId: "oauth-" + req.GetName()},
	}, nil
}

// memKeyStore is an in-memory KeyStore.
type memKeyStore struct {
	keys      map[string]string
	discarded []string
}

func newMemKeyStore() *memKeyStore { return &memKeyStore{keys: map[string]string{}} }

func (m *memKeyStore) PrivateKey(service string) (string, bool, error) {
	pem, ok := m.keys[service]
	return pem, ok, nil
}

func (m *memKeyStore) Record(_ context.Context, service, pem string) error {
	if _, exists := m.keys[service]; exists {
		return nil
	}
	m.keys[service] = pem
	return nil
}

func (m *memKeyStore) Discard(service string) error {
	delete(m.keys, service)
	m.discarded = append(m.discarded, service)
	return nil
}

type memRegistry struct {
	entries []string
	err     error
}

func (m *memRegistry) EnsureRegistered(_ context.Context, name, kind string, isSystem bool) error {
	if m.err != nil {
		return m.err
	}
	sys := "app"
	if isSystem {
		sys = "system"
	}
	m.entries = append(m.entries, name+"/"+kind+"/"+sys)
	return nil
}

func newTestApplier(auth *fakeAuth, keys KeyStore, reg Registry) *Applier {
	return newApplier(applierDeps{
		tenantID:    "tenant-uuid",
		services:    auth,
		apis:        auth,
		permissions: auth,
		policies:    auth,
		clients:     auth,
		keys:        keys,
		registry:    reg,
	})
}

func testCatalog() steward.Catalog {
	return steward.Catalog{Objects: []steward.Object{
		{APIVersion: steward.APIVersion, Kind: steward.KindService, Metadata: steward.Meta{Name: "storage"},
			Spec: steward.ServiceSpec{DisplayName: "Maintainerd Storage", Version: "v1", RegistryKind: "Storage", Tier: steward.TierSystem}},
		{APIVersion: steward.APIVersion, Kind: steward.KindResourceAPI, Metadata: steward.Meta{Name: "storage-api"},
			Spec: steward.ResourceAPISpec{Service: "storage", Identifier: "https://storage.local", DisplayName: "Storage API",
				Permissions: []steward.Permission{{Name: "storage:Get"}, {Name: "storage:Put"}}}},
		{APIVersion: steward.APIVersion, Kind: steward.KindServiceClient, Metadata: steward.Meta{Name: "storage-control"},
			Spec: steward.ServiceClientSpec{Service: "storage", Audience: "https://storage.local", AllowedScopes: []string{"secret:GetSecret"}}},
		{APIVersion: steward.APIVersion, Kind: steward.KindServicePolicy, Metadata: steward.Meta{Name: "storage-service"},
			Spec: steward.ServicePolicySpec{Service: "storage", PolicyName: "storage-service", AllowedActions: []string{"secret:GetSecret"}}},
	}}
}

func statusOf(outcomes []Outcome, kind, name string) Status {
	for _, o := range outcomes {
		if o.Kind == kind && o.Name == name {
			return o.Status
		}
	}
	return ""
}

func TestApplierCreatesEveryObjectOnAFreshAuth(t *testing.T) {
	auth := newFakeAuth()
	keys := newMemKeyStore()
	reg := &memRegistry{}
	applier := newTestApplier(auth, keys, reg)

	totals, err := steward.Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)
	assert.Equal(t, steward.Report{Services: 1, APIs: 1, Clients: 1, Policies: 1, NewKeys: 1}, totals)

	assert.Len(t, auth.createdServices, 1)
	assert.Len(t, auth.createdAPIs, 1)
	assert.Len(t, auth.createdPermissions, 2)
	assert.Len(t, auth.createdClients, 1)
	assert.Len(t, auth.createdPolicies, 1)
	assert.Len(t, auth.assignments, 1)

	for _, o := range applier.Outcomes() {
		assert.Equal(t, StatusCreated, o.Status, "%s %s", o.Kind, o.Name)
	}

	// Registry convergence: core's own row for the capability, with the kind and
	// tier the catalog declares.
	assert.Equal(t, []string{"storage/Storage/system"}, reg.entries)

	// The policy document is the enumerated allow statement auth expects.
	doc := auth.createdPolicies[0].GetDocument().AsMap()
	assert.Equal(t, "v1", doc["version"])
	statements, ok := doc["statement"].([]any)
	require.True(t, ok)
	require.Len(t, statements, 1)
	stmt := statements[0].(map[string]any)
	assert.Equal(t, "allow", stmt["effect"])
	assert.Equal(t, []any{"secret:GetSecret"}, stmt["action"])
	assert.Equal(t, []any{"*"}, stmt["resource"])
}

func TestApplierIsIdempotent(t *testing.T) {
	auth := newFakeAuth()
	keys := newMemKeyStore()

	_, err := steward.Reconcile(context.Background(), testCatalog(), newTestApplier(auth, keys, &memRegistry{}), keys)
	require.NoError(t, err)

	before := struct{ services, apis, perms, clients, policies int }{
		len(auth.createdServices), len(auth.createdAPIs), len(auth.createdPermissions),
		len(auth.createdClients), len(auth.createdPolicies),
	}

	second := newTestApplier(auth, keys, &memRegistry{})
	totals, err := steward.Reconcile(context.Background(), testCatalog(), second, keys)
	require.NoError(t, err)

	// A converged catalog performs ZERO writes on the second pass.
	assert.Len(t, auth.createdServices, before.services)
	assert.Len(t, auth.createdAPIs, before.apis)
	assert.Len(t, auth.createdPermissions, before.perms)
	assert.Len(t, auth.createdClients, before.clients)
	assert.Len(t, auth.createdPolicies, before.policies)

	// ...and reports Ensured, not Created.
	for _, o := range second.Outcomes() {
		assert.Equal(t, StatusEnsured, o.Status, "%s %s", o.Kind, o.Name)
	}
	// No key is minted twice: the stored PEM is reused.
	assert.Equal(t, 0, totals.NewKeys)
}

func TestApplierBindsClientToServiceWithoutAnIdentityProvider(t *testing.T) {
	auth := newFakeAuth()
	keys := newMemKeyStore()

	_, err := steward.Reconcile(context.Background(), testCatalog(), newTestApplier(auth, keys, &memRegistry{}), keys)
	require.NoError(t, err)

	require.Len(t, auth.createdClients, 1)
	req := auth.createdClients[0]

	// service_id is the binding that puts `svc` in this client's tokens.
	require.NotNil(t, req.ServiceId)
	assert.Equal(t, auth.services["storage"].GetServiceId(), req.GetServiceId())

	// identity_provider_id must stay unset so auth defaults it to the built-in
	// provider — naming one would bind a machine credential to a human login
	// source.
	assert.Empty(t, req.GetIdentityProviderId())

	assert.Equal(t, "m2m", req.GetClientType())
	cfg := req.GetConfig().AsMap()
	assert.Equal(t, "private_key_jwt", cfg["token_endpoint_auth_method"])
	assert.Equal(t, []any{"client_credentials"}, cfg["grant_types"])
	// A machine credential with no scope allowlist is unbounded, so the catalog's
	// grants travel with it.
	assert.Equal(t, []any{"secret:GetSecret"}, cfg["allowed_scopes"])
	// Only the PUBLIC key set reaches auth.
	jwks, ok := cfg["jwks"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, jwks["keys"])
	assert.NotContains(t, req.String(), "PRIVATE KEY")
}

func TestApplierFallsBackToTheAudienceWhenNoScopesAreDeclared(t *testing.T) {
	auth := newFakeAuth()
	keys := newMemKeyStore()
	cat := testCatalog()
	spec := cat.Objects[2].Spec.(steward.ServiceClientSpec)
	spec.AllowedScopes = nil
	cat.Objects[2].Spec = spec

	_, err := steward.Reconcile(context.Background(), cat, newTestApplier(auth, keys, &memRegistry{}), keys)
	require.NoError(t, err)

	cfg := auth.createdClients[0].GetConfig().AsMap()
	assert.Equal(t, []any{"https://storage.local"}, cfg["allowed_scopes"])
}

func TestApplierIsolatesPerObjectFailures(t *testing.T) {
	auth := newFakeAuth()
	auth.failOn["CreateAPI:storage-api"] = errors.New("auth said no")
	keys := newMemKeyStore()
	applier := newTestApplier(auth, keys, &memRegistry{})

	// The pass does NOT abort: a later object that does not depend on the failed
	// one still converges.
	_, err := steward.Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)

	outcomes := applier.Outcomes()
	assert.Equal(t, StatusCreated, statusOf(outcomes, "Service", "storage"))
	assert.Equal(t, StatusFailed, statusOf(outcomes, "ResourceAPI", "storage-api"))
	assert.Equal(t, StatusCreated, statusOf(outcomes, "ServiceClient", "storage-control"))
	assert.Equal(t, StatusCreated, statusOf(outcomes, "ServicePolicy", "storage-service"))

	// The failure is recorded, not swallowed.
	var failed Outcome
	for _, o := range outcomes {
		if o.Status == StatusFailed {
			failed = o
		}
	}
	assert.Contains(t, failed.Error, "auth said no")
}

func TestApplierRecordsDependencyFailuresWithoutAborting(t *testing.T) {
	auth := newFakeAuth()
	auth.failOn["CreateService:storage"] = errors.New("service refused")
	keys := newMemKeyStore()
	applier := newTestApplier(auth, keys, &memRegistry{})

	_, err := steward.Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)

	// Everything downstream of the missing service fails cleanly rather than
	// creating orphaned objects.
	for _, o := range applier.Outcomes() {
		assert.Equal(t, StatusFailed, o.Status, "%s %s", o.Kind, o.Name)
	}
	assert.Empty(t, auth.createdClients)
	assert.Empty(t, auth.createdPolicies)
}

func TestApplierRefusesToAdoptAnExistingClientItHoldsNoKeyFor(t *testing.T) {
	auth := newFakeAuth()
	// Auth already holds a client under this name, registered against a JWKS
	// core cannot prove it owns.
	auth.clients["storage-control"] = &authv1.Client{ClientId: "pre-existing", Name: "storage-control"}
	keys := newMemKeyStore()
	applier := newTestApplier(auth, keys, &memRegistry{})

	_, err := steward.Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)

	assert.Equal(t, StatusFailed, statusOf(applier.Outcomes(), "ServiceClient", "storage-control"))
	assert.Empty(t, auth.createdClients)
	// No key is minted for a client we cannot register.
	assert.Empty(t, keys.keys)
}

func TestApplierDiscardsAFreshKeyWhenRegistrationFails(t *testing.T) {
	auth := newFakeAuth()
	auth.failOn["CreateClient:storage-control"] = errors.New("rejected")
	keys := newMemKeyStore()
	applier := newTestApplier(auth, keys, &memRegistry{})

	_, err := steward.Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)

	assert.Equal(t, StatusFailed, statusOf(applier.Outcomes(), "ServiceClient", "storage-control"))
	// The orphan is rolled back, or the next pass would believe a matching key
	// exists for a client auth never accepted.
	assert.Equal(t, []string{"storage"}, keys.discarded)
	assert.Empty(t, keys.keys)
}

func TestApplierAddsOnlyMissingPermissions(t *testing.T) {
	auth := newFakeAuth()
	keys := newMemKeyStore()

	// Pre-seed the API with one of the two catalog permissions.
	api := &authv1.API{ApiId: "api-existing", Name: "storage-api"}
	auth.apis["storage-api"] = api
	auth.permissions["api-existing"] = []*authv1.Permission{{PermissionId: "p1", Name: "storage:Get"}}

	applier := newTestApplier(auth, keys, &memRegistry{})
	_, err := steward.Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)

	require.Len(t, auth.createdPermissions, 1)
	assert.Equal(t, "storage:Put", auth.createdPermissions[0].GetName())
	// An API that gained a permission counts as changed, not merely ensured.
	assert.Equal(t, StatusCreated, statusOf(applier.Outcomes(), "ResourceAPI", "storage-api"))
}

func TestApplierSkipsPolicyForAServiceWithNoOutboundGrants(t *testing.T) {
	auth := newFakeAuth()
	keys := newMemKeyStore()
	cat := testCatalog()
	spec := cat.Objects[3].Spec.(steward.ServicePolicySpec)
	spec.AllowedActions = nil
	cat.Objects[3].Spec = spec

	applier := newTestApplier(auth, keys, &memRegistry{})
	_, err := steward.Reconcile(context.Background(), cat, applier, keys)
	require.NoError(t, err)

	// No empty policy is created, and nothing is attached: an empty grant means
	// nothing and only invites being widened later.
	assert.Empty(t, auth.createdPolicies)
	assert.Empty(t, auth.assignments)
	assert.Equal(t, StatusEnsured, statusOf(applier.Outcomes(), "ServicePolicy", "storage-service"))
}

func TestApplierSkipsRegistryConvergenceWithoutARegistryKind(t *testing.T) {
	auth := newFakeAuth()
	keys := newMemKeyStore()
	reg := &memRegistry{}
	cat := testCatalog()
	spec := cat.Objects[0].Spec.(steward.ServiceSpec)
	spec.RegistryKind = ""
	cat.Objects[0].Spec = spec

	_, err := steward.Reconcile(context.Background(), cat, newTestApplier(auth, keys, reg), keys)
	require.NoError(t, err)
	assert.Empty(t, reg.entries)
}

func TestApplierNeverTouchesSurfacesOutsideItsBoundary(t *testing.T) {
	// The provisioning boundary is enforced by construction: the applier holds
	// only the five seams below. If an identity-provider, user, tenant-member,
	// security-settings or branding client ever appears here, the boundary
	// documented in doc.go has been crossed.
	applier := newTestApplier(newFakeAuth(), newMemKeyStore(), &memRegistry{})
	assert.NotNil(t, applier.services)
	assert.NotNil(t, applier.apis)
	assert.NotNil(t, applier.permissions)
	assert.NotNil(t, applier.policies)
	assert.NotNil(t, applier.clients)
	assert.Nil(t, applier.registry.(*memRegistry).err)
}
