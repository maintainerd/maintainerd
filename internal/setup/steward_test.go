package setup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	authv1 "github.com/maintainerd/core/gen/maintainerd/auth/v1"
	"github.com/maintainerd/core/internal/steward"
)

type fakeStewardSetupClient struct {
	registered    []*authv1.RegisterControlServiceRequest
	apis          []*authv1.EnsureResourceAPIRequest
	clients       []*authv1.EnsureControlClientRequest
	clientExisted bool
}

func (f *fakeStewardSetupClient) RegisterControlService(_ context.Context, req *authv1.RegisterControlServiceRequest, _ ...grpc.CallOption) (*authv1.RegisterControlServiceResponse, error) {
	f.registered = append(f.registered, req)
	return &authv1.RegisterControlServiceResponse{ServiceId: "svc-1", PolicyId: "pol-1", PolicyName: req.GetPolicyName()}, nil
}

func (f *fakeStewardSetupClient) EnsureControlClient(_ context.Context, req *authv1.EnsureControlClientRequest, _ ...grpc.CallOption) (*authv1.EnsureControlClientResponse, error) {
	f.clients = append(f.clients, req)
	return &authv1.EnsureControlClientResponse{
		ClientId:       "client-1",
		OauthClientId:  "oauth-1",
		AlreadyExisted: f.clientExisted,
	}, nil
}

func (f *fakeStewardSetupClient) EnsureResourceAPI(_ context.Context, req *authv1.EnsureResourceAPIRequest, _ ...grpc.CallOption) (*authv1.EnsureResourceAPIResponse, error) {
	f.apis = append(f.apis, req)
	return &authv1.EnsureResourceAPIResponse{ServiceId: "svc-1", ApiId: "api-1", Identifier: req.GetIdentifier()}, nil
}

func TestAuthStewardApplierRecordsAndReusesServiceClientKeys(t *testing.T) {
	dir := t.TempDir()
	keys := newStewardKeyStore(dir)
	cat := steward.Catalog{Objects: []steward.Object{
		{APIVersion: steward.APIVersion, Kind: steward.KindService, Metadata: steward.Meta{Name: "worker"}, Spec: steward.ServiceSpec{DisplayName: "Worker", Version: "v1"}},
		{APIVersion: steward.APIVersion, Kind: steward.KindResourceAPI, Metadata: steward.Meta{Name: "worker-api"}, Spec: steward.ResourceAPISpec{
			Service:     "worker",
			Identifier:  "https://worker.local",
			DisplayName: "Worker API",
			Permissions: []steward.Permission{{Name: "worker:Run", Description: "Run worker jobs"}},
		}},
		{APIVersion: steward.APIVersion, Kind: steward.KindServiceClient, Metadata: steward.Meta{Name: "worker-control"}, Spec: steward.ServiceClientSpec{Service: "worker", Audience: "https://worker.local"}},
		{APIVersion: steward.APIVersion, Kind: steward.KindServicePolicy, Metadata: steward.Meta{Name: "worker-service"}, Spec: steward.ServicePolicySpec{Service: "worker", PolicyName: "worker-service", AllowedActions: []string{"worker:Run"}}},
	}}

	firstClient := &fakeStewardSetupClient{}
	report, err := steward.Reconcile(context.Background(), cat, newAuthStewardApplier(firstClient, keys), keys)
	require.NoError(t, err)
	assert.Equal(t, 1, report.NewKeys)
	assert.Len(t, firstClient.apis, 1)
	assert.Len(t, firstClient.clients, 1)
	assert.Len(t, firstClient.registered, 1)
	assert.Equal(t, []string{"worker:Run"}, firstClient.registered[0].GetAllowedActions())

	keyPath := filepath.Join(dir, "worker.pem")
	firstKey, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	require.NotEmpty(t, firstKey)

	secondClient := &fakeStewardSetupClient{clientExisted: true}
	report, err = steward.Reconcile(context.Background(), cat, newAuthStewardApplier(secondClient, keys), keys)
	require.NoError(t, err)
	assert.Equal(t, 0, report.NewKeys)
	secondKey, err := os.ReadFile(keyPath)
	require.NoError(t, err)
	assert.Equal(t, string(firstKey), string(secondKey))
}

func TestAuthStewardApplierSkipsEmptyServicePolicy(t *testing.T) {
	client := &fakeStewardSetupClient{}
	applier := newAuthStewardApplier(client, newStewardKeyStore(t.TempDir()))
	require.NoError(t, applier.EnsureService(context.Background(), "secret", steward.ServiceSpec{DisplayName: "Secret"}))
	require.NoError(t, applier.EnsureServicePolicy(context.Background(), "secret-service", steward.ServicePolicySpec{Service: "secret"}))
	assert.Empty(t, client.registered, "empty policies must not call RegisterControlService and receive Auth's default grants")
}
