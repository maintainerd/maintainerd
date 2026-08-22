package steward

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeApplier struct {
	calls       []string
	errOn       string
	generated   map[string]*GeneratedClient
	lastService ServiceSpec
	lastAPI     ResourceAPISpec
	lastClient  ServiceClientSpec
	lastPolicy  ServicePolicySpec
}

func (f *fakeApplier) fail(call string) error {
	f.calls = append(f.calls, call)
	if f.errOn == call {
		return errors.New("boom")
	}
	return nil
}

func (f *fakeApplier) EnsureService(_ context.Context, name string, spec ServiceSpec) error {
	f.lastService = spec
	return f.fail("service:" + name)
}

func (f *fakeApplier) EnsureResourceAPI(_ context.Context, name string, spec ResourceAPISpec) error {
	f.lastAPI = spec
	return f.fail("api:" + name)
}

func (f *fakeApplier) EnsureServiceClient(_ context.Context, name string, spec ServiceClientSpec) (*GeneratedClient, error) {
	f.lastClient = spec
	if err := f.fail("client:" + name); err != nil {
		return nil, err
	}
	if f.generated != nil {
		return f.generated[name], nil
	}
	return nil, nil
}

func (f *fakeApplier) EnsureServicePolicy(_ context.Context, name string, spec ServicePolicySpec) error {
	f.lastPolicy = spec
	return f.fail("policy:" + name)
}

type fakeKeySink struct {
	records []string
	err     error
}

func (f *fakeKeySink) Record(_ context.Context, service string, privateKeyPEM string) error {
	f.records = append(f.records, service+":"+privateKeyPEM)
	return f.err
}

func testCatalog() Catalog {
	return Catalog{Objects: []Object{
		{APIVersion: APIVersion, Kind: KindService, Metadata: Meta{Name: "secret"}, Spec: ServiceSpec{DisplayName: "Secret"}},
		{APIVersion: APIVersion, Kind: KindResourceAPI, Metadata: Meta{Name: "secret-api"}, Spec: ResourceAPISpec{Service: "secret", Identifier: "secret", Permissions: []Permission{{Name: "secret:GetSecret"}}}},
		{APIVersion: APIVersion, Kind: KindServiceClient, Metadata: Meta{Name: "secret-control"}, Spec: ServiceClientSpec{Service: "secret", Audience: "secret"}},
		{APIVersion: APIVersion, Kind: KindServicePolicy, Metadata: Meta{Name: "secret-service"}, Spec: ServicePolicySpec{Service: "secret", PolicyName: "secret-service", AllowedActions: []string{"secret:GetSecret"}}},
	}}
}

func TestReconcileAppliesObjectsInCatalogOrder(t *testing.T) {
	applier := &fakeApplier{}
	keys := &fakeKeySink{}

	rep, err := Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)

	assert.Equal(t, Report{Services: 1, APIs: 1, Clients: 1, Policies: 1}, rep)
	assert.Equal(t, []string{
		"service:secret",
		"api:secret-api",
		"client:secret-control",
		"policy:secret-service",
	}, applier.calls)
	assert.Empty(t, keys.records)
}

func TestReconcileRecordsFreshServiceClientKeys(t *testing.T) {
	applier := &fakeApplier{
		generated: map[string]*GeneratedClient{
			"secret-control": {ClientID: "client-1", OAuthClientID: "oauth-1", PrivateKeyPEM: "pem"},
		},
	}
	keys := &fakeKeySink{}

	rep, err := Reconcile(context.Background(), testCatalog(), applier, keys)
	require.NoError(t, err)

	assert.Equal(t, 1, rep.NewKeys)
	assert.Equal(t, []string{"secret:pem"}, keys.records)
}

func TestReconcileFailsWhenGeneratedKeyHasNoSink(t *testing.T) {
	applier := &fakeApplier{
		generated: map[string]*GeneratedClient{
			"secret-control": {PrivateKeyPEM: "pem"},
		},
	}

	rep, err := Reconcile(context.Background(), testCatalog(), applier, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no KeySink")
	assert.Equal(t, 1, rep.Clients)
}

func TestReconcileStopsAtFirstError(t *testing.T) {
	applier := &fakeApplier{errOn: "api:secret-api"}

	rep, err := Reconcile(context.Background(), testCatalog(), applier, &fakeKeySink{})
	require.Error(t, err)

	assert.Contains(t, err.Error(), "ensure ResourceAPI")
	assert.Equal(t, Report{Services: 1}, rep)
	assert.Equal(t, []string{"service:secret", "api:secret-api"}, applier.calls)
}

func TestReconcileRequiresApplier(t *testing.T) {
	_, err := Reconcile(context.Background(), testCatalog(), nil, &fakeKeySink{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Applier is required")
}

func TestReconcileRejectsMalformedObjects(t *testing.T) {
	tests := []struct {
		name string
		obj  Object
		want string
	}{
		{
			name: "unsupported apiVersion",
			obj:  Object{APIVersion: "v0", Kind: KindService, Metadata: Meta{Name: "secret"}, Spec: ServiceSpec{}},
			want: "unsupported apiVersion",
		},
		{
			name: "empty name",
			obj:  Object{APIVersion: APIVersion, Kind: KindService, Spec: ServiceSpec{}},
			want: "empty metadata.name",
		},
		{
			name: "nil spec",
			obj:  Object{APIVersion: APIVersion, Kind: KindService, Metadata: Meta{Name: "secret"}},
			want: "nil spec",
		},
		{
			name: "kind mismatch",
			obj:  Object{APIVersion: APIVersion, Kind: KindService, Metadata: Meta{Name: "secret"}, Spec: ResourceAPISpec{}},
			want: "does not match spec kind",
		},
		{
			name: "wildcard action",
			obj:  Object{APIVersion: APIVersion, Kind: KindServicePolicy, Metadata: Meta{Name: "secret-service"}, Spec: ServicePolicySpec{AllowedActions: []string{"*"}}},
			want: "bare *",
		},
		{
			name: "empty action",
			obj:  Object{APIVersion: APIVersion, Kind: KindServicePolicy, Metadata: Meta{Name: "secret-service"}, Spec: ServicePolicySpec{AllowedActions: []string{""}}},
			want: "empty action",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Reconcile(context.Background(), Catalog{Objects: []Object{tt.obj}}, &fakeApplier{}, &fakeKeySink{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
