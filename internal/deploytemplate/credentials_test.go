package deploytemplate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maintainerd/core/internal/storage"
)

// fakeSink records what a render tried to store, so a test can assert both what
// reached the vault and — the part that matters — what did not reach the spec.
type fakeSink struct {
	err    error
	stored map[string][]byte
}

func (f *fakeSink) Store(_ context.Context, template, param string, value []byte) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.stored == nil {
		f.stored = map[string][]byte{}
	}
	key := template + "/" + param
	f.stored[key] = append([]byte(nil), value...)
	return "secret://vault/" + key, nil
}

// secretTemplateRepo builds a template whose Env carries a secret-typed
// parameter — the one place a credential would otherwise land in a container's
// environment.
func secretTemplateRepo(t *testing.T, params []Parameter) *fakeRepo {
	t.Helper()
	paramJSON, err := json.Marshal(params)
	require.NoError(t, err)
	return &fakeRepo{row: storage.DeploymentTemplate{
		TemplateUuid: uuid.New(),
		Name:         "api",
		Version:      "v1",
		Image:        "api:1",
		Parameters:   paramJSON,
		Spec:         []byte(`{"name":"api","env":{"DB_PASSWORD":"{{ .db_password }}"}}`),
		Metadata:     []byte(`{}`),
	}}
}

// THE SECURITY-CRITICAL ONE. A provision that needs to write a credential to
// maintainerd-secret must FAIL when secret is unavailable, and must not fall back
// to injecting the credential as a plain environment variable. Both halves are
// asserted: the error, and the absence of any rendered spec at all.
func TestRenderFailsClosedWhenTheVaultIsUnavailable(t *testing.T) {
	repo := secretTemplateRepo(t, []Parameter{{Name: "db_password", Type: "secret", Generate: true}})
	sink := &fakeSink{err: errors.New("secretctrl: maintainerd-secret is unavailable")}

	rendered, err := NewService(repo, sink).Render(context.Background(), RenderInput{Name: "api"})

	require.Error(t, err)
	assert.Nil(t, rendered, "an unreachable vault must yield NO spec — a partially rendered one is a spec someone will deploy")
	assert.Contains(t, err.Error(), "db_password")
	assert.Contains(t, err.Error(), ErrSecretMaterialUnavailable.Error())
}

// The same rule for a value the CALLER supplied: an unreachable vault must not
// make core inline it instead.
func TestRenderFailsClosedForASuppliedSecretValue(t *testing.T) {
	const plaintext = "sup3r-s3cret-value"
	repo := secretTemplateRepo(t, []Parameter{{Name: "db_password", Type: "secret", Required: true}})
	sink := &fakeSink{err: errors.New("secretctrl: maintainerd-secret is unavailable")}

	rendered, err := NewService(repo, sink).Render(context.Background(), RenderInput{
		Name:   "api",
		Params: map[string]any{"db_password": plaintext},
	})

	require.Error(t, err)
	require.Nil(t, rendered)
	// The value must not travel in the error either — errors are logged and some
	// are surfaced over HTTP.
	assert.NotContains(t, err.Error(), plaintext)
}

// With no sink wired at all (development, no SECRET_BASE_URL) the answer is the
// same refusal — there is deliberately no third branch in which the value
// reaches the spec.
func TestRenderFailsClosedWithNoVaultConfigured(t *testing.T) {
	repo := secretTemplateRepo(t, []Parameter{{Name: "db_password", Type: "secret", Generate: true}})

	rendered, err := NewService(repo, nil).Render(context.Background(), RenderInput{Name: "api"})

	require.Error(t, err)
	require.Nil(t, rendered)
	assert.Contains(t, err.Error(), "SECRET_BASE_URL")
}

// The success path's real assertion: the plaintext appears NOWHERE in the
// rendered artifact — not in Env, not in Params, not anywhere in the serialized
// spec that gets persisted and shipped to an agent.
func TestRenderedSpecNeverCarriesThePlaintext(t *testing.T) {
	const plaintext = "sup3r-s3cret-value"
	repo := secretTemplateRepo(t, []Parameter{{Name: "db_password", Type: "secret", Required: true}})
	sink := &fakeSink{}

	rendered, err := NewService(repo, sink).Render(context.Background(), RenderInput{
		Name:   "api",
		Params: map[string]any{"db_password": plaintext},
	})
	require.NoError(t, err)

	assert.Equal(t, "secret://vault/api/db_password", rendered.Spec.Env["DB_PASSWORD"])
	assert.Equal(t, "secret://vault/api/db_password", rendered.Params["db_password"])
	assert.Equal(t, plaintext, string(sink.stored["api/db_password"]), "the vault is the one place the value goes")

	blob, err := json.Marshal(rendered)
	require.NoError(t, err)
	assert.NotContains(t, string(blob), plaintext,
		"the rendered artifact is persisted and shipped to an agent; a plaintext anywhere in it is a leak")
}

// A generated credential must actually be generated — real entropy, not the
// parameter name — and it must be the value the vault received.
func TestGeneratedCredentialHasEntropy(t *testing.T) {
	repo := secretTemplateRepo(t, []Parameter{{Name: "db_password", Type: "secret", Generate: true}})
	first := &fakeSink{}
	second := &fakeSink{}

	_, err := NewService(repo, first).Render(context.Background(), RenderInput{Name: "api"})
	require.NoError(t, err)
	_, err = NewService(repo, second).Render(context.Background(), RenderInput{Name: "api"})
	require.NoError(t, err)

	a := string(first.stored["api/db_password"])
	b := string(second.stored["api/db_password"])
	assert.NotEmpty(t, a)
	assert.NotEqual(t, "db_password", a)
	assert.NotEqual(t, a, b, "two provisions must not share a credential")
	assert.GreaterOrEqual(t, len(a), 32)
}

// A default on a secret parameter is a hardcoded credential shared by every
// install that skipped the parameter, so it is refused rather than honoured.
func TestSecretParameterMayNotDeclareADefault(t *testing.T) {
	repo := secretTemplateRepo(t, []Parameter{{Name: "db_password", Type: "secret", Default: "changeme"}})

	_, err := NewService(repo, &fakeSink{}).Render(context.Background(), RenderInput{Name: "api"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "default")
}

// secretRef is idempotent: templates wrap the parameter in it, and the parameter
// is already a full reference by the time the template runs.
func TestSecretRefIsIdempotent(t *testing.T) {
	assert.Equal(t, "secret://a/b/c", secretRefFunc("secret://a/b/c"))
	assert.Equal(t, "secret://plain", secretRefFunc("plain"))
	assert.True(t, strings.HasPrefix(secretRefFunc("x"), secretRefPrefix))
}
