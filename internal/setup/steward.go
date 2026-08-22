package setup

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	authv1 "github.com/maintainerd/core/gen/maintainerd/auth/v1"
	"github.com/maintainerd/core/internal/steward"
)

type stewardSetupClient interface {
	RegisterControlService(context.Context, *authv1.RegisterControlServiceRequest, ...grpcCallOption) (*authv1.RegisterControlServiceResponse, error)
	EnsureControlClient(context.Context, *authv1.EnsureControlClientRequest, ...grpcCallOption) (*authv1.EnsureControlClientResponse, error)
	EnsureResourceAPI(context.Context, *authv1.EnsureResourceAPIRequest, ...grpcCallOption) (*authv1.EnsureResourceAPIResponse, error)
}

type grpcCallOption = grpc.CallOption

type authStewardApplier struct {
	cli      stewardSetupClient
	keys     *steward.FileKeyStore
	services map[string]steward.ServiceSpec
}

func newAuthStewardApplier(cli stewardSetupClient, keys *steward.FileKeyStore) *authStewardApplier {
	return &authStewardApplier{cli: cli, keys: keys, services: map[string]steward.ServiceSpec{}}
}

// newStewardKeyStore builds the per-service key store the setup-window applier
// writes through. It is the SAME store the post-setup regular-surface applier
// reads (see internal/steward.FileKeyStore).
func newStewardKeyStore(dir string) *steward.FileKeyStore { return steward.NewFileKeyStore(dir) }

func (o *Orchestrator) reconcileSteward(ctx context.Context, cli authv1.SetupServiceClient) error {
	keys := newStewardKeyStore(o.cfg.StewardKeyDir)
	_, err := steward.Reconcile(ctx, steward.BuiltinCatalog(o.cfg), newAuthStewardApplier(cli, keys), keys)
	return err
}

func (a *authStewardApplier) EnsureService(_ context.Context, name string, spec steward.ServiceSpec) error {
	a.services[name] = spec
	return nil
}

func (a *authStewardApplier) EnsureResourceAPI(ctx context.Context, name string, spec steward.ResourceAPISpec) error {
	perms := make([]*authv1.EnsureResourceAPIPermission, 0, len(spec.Permissions))
	for _, p := range spec.Permissions {
		perms = append(perms, &authv1.EnsureResourceAPIPermission{Name: p.Name, Description: p.Description})
	}
	display := spec.DisplayName
	if svc, ok := a.services[spec.Service]; ok && svc.DisplayName != "" {
		display = svc.DisplayName
	}
	_, err := a.cli.EnsureResourceAPI(ctx, &authv1.EnsureResourceAPIRequest{
		ServiceName:        spec.Service,
		ServiceDisplayName: display,
		Name:               name,
		DisplayName:        spec.DisplayName,
		Identifier:         spec.Identifier,
		Permissions:        perms,
	})
	return err
}

func (a *authStewardApplier) EnsureServiceClient(ctx context.Context, name string, spec steward.ServiceClientSpec) (*steward.GeneratedClient, error) {
	privatePEM, existed, err := a.keys.PrivateKey(spec.Service)
	if err != nil {
		return nil, err
	}
	generated := false
	if privatePEM == "" {
		privatePEM, _, err = generateControlKey()
		if err != nil {
			return nil, err
		}
		if err := a.keys.Record(ctx, spec.Service, privatePEM); err != nil {
			return nil, err
		}
		generated = true
	}
	jwks, err := jwksFromPrivatePEM(privatePEM)
	if err != nil {
		return nil, err
	}
	resp, err := a.cli.EnsureControlClient(ctx, &authv1.EnsureControlClientRequest{
		Name:        name,
		DisplayName: name,
		ServiceName: spec.Service,
		Jwks:        jwks,
		Audience:    spec.Audience,
	})
	if err != nil {
		return nil, err
	}
	if resp.GetAlreadyExisted() && !existed {
		if generated {
			_ = a.keys.Discard(spec.Service)
		}
		return nil, fmt.Errorf("auth client %q already exists but no local private key was found for service %q", name, spec.Service)
	}
	out := &steward.GeneratedClient{
		ClientID:       resp.GetClientId(),
		OAuthClientID:  resp.GetOauthClientId(),
		AlreadyExisted: resp.GetAlreadyExisted(),
	}
	if !resp.GetAlreadyExisted() && generated {
		out.PrivateKeyPEM = privatePEM
	}
	return out, nil
}

func (a *authStewardApplier) EnsureServicePolicy(ctx context.Context, name string, spec steward.ServicePolicySpec) error {
	if len(spec.AllowedActions) == 0 {
		return nil
	}
	svc := a.services[spec.Service]
	_, err := a.cli.RegisterControlService(ctx, &authv1.RegisterControlServiceRequest{
		Name:           spec.Service,
		DisplayName:    firstNonEmpty(svc.DisplayName, spec.Service),
		Description:    svc.Description,
		Version:        firstNonEmpty(svc.Version, "v1"),
		AllowedActions: spec.AllowedActions,
		PolicyName:     firstNonEmpty(spec.PolicyName, name),
	})
	return err
}
