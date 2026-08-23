package secretctrl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticTokens struct {
	token string
	err   error
}

func (s staticTokens) Token(context.Context) (string, error) { return s.token, s.err }

func newTestClient(t *testing.T, baseURL string, tokens TokenSource) *Client {
	t.Helper()
	c := New(Config{BaseURL: baseURL, Audience: "https://secret.example.com", Timeout: 2 * time.Second}, tokens)
	require.NotNil(t, c)
	return c
}

// A nil client is a fully unavailable one, not a no-op that lets a caller
// through. Development runs with no vault, and the provisioning path must take
// the same refusing branch there as it does against a vault that is down.
func TestUnconfiguredClientFailsClosed(t *testing.T) {
	c := New(Config{}, nil)
	require.Nil(t, c)
	assert.False(t, c.Configured())
	require.ErrorIs(t, c.Probe(context.Background()), ErrNotConfigured)
	require.ErrorIs(t, c.PutCredential(context.Background(),
		Address{Project: "p", Environment: "e", Key: "k"}, []byte("v"), ""), ErrNotConfigured)
}

func TestProbeReadsReadyz(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		var path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		}))
		defer srv.Close()

		require.NoError(t, newTestClient(t, srv.URL, nil).Probe(context.Background()))
		assert.Equal(t, "/readyz", path, "readiness, not liveness: a vault that is up but cannot serve is no use to a provision")
	})

	t.Run("not ready is unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		require.ErrorIs(t, newTestClient(t, srv.URL, nil).Probe(context.Background()), ErrUnavailable)
	})

	t.Run("closed port is unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		require.ErrorIs(t, newTestClient(t, url, nil).Probe(context.Background()), ErrUnavailable)
	})
}

func TestPutCredentialWritesTheVaultAddress(t *testing.T) {
	var got struct {
		Project       string `json:"project"`
		Environment   string `json:"environment"`
		FolderPath    string `json:"folder_path"`
		Key           string `json:"key"`
		Value         string `json:"value"`
		CreateFolders bool   `json:"create_folders"`
	}
	var authorization, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	addr := Address{Project: "default", Environment: "default", FolderPath: "api", Key: "db_password"}
	err := newTestClient(t, srv.URL, staticTokens{token: "minted-token"}).
		PutCredential(context.Background(), addr, []byte("plaintext"), "provisioned")
	require.NoError(t, err)

	assert.Equal(t, "/api/v1/secrets", path)
	assert.Equal(t, "Bearer minted-token", authorization)
	assert.Equal(t, "default", got.Project)
	assert.Equal(t, "api", got.FolderPath)
	assert.Equal(t, "db_password", got.Key)
	assert.True(t, got.CreateFolders)
	decoded, derr := base64.StdEncoding.DecodeString(got.Value)
	require.NoError(t, derr)
	assert.Equal(t, "plaintext", string(decoded))
	assert.Equal(t, "secret://default/default/api/db_password", addr.Ref())
}

// A 5xx or a rate limit is the same class as unreachable — a caller deciding
// whether to abort a provision does not care which.
func TestPutCredentialTreatsServerFailuresAsUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		err := newTestClient(t, srv.URL, staticTokens{token: "t"}).PutCredential(context.Background(),
			Address{Project: "p", Environment: "e", Key: "k"}, []byte("v"), "")
		srv.Close()
		require.ErrorIs(t, err, ErrUnavailable)
	}
}

// A 4xx is core's fault (wrong audience, missing grant) and retrying will not fix
// it, so it is NOT ErrUnavailable — but it is still an error, so the provision
// still fails.
func TestPutCredentialDoesNotRetryOnADenial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL, staticTokens{token: "t"}).PutCredential(context.Background(),
		Address{Project: "p", Environment: "e", Key: "k"}, []byte("v"), "")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "403")
}

// NEVER LOG A SECRET. No error from this package may carry the value or the
// bearer token, because every one of them is logged and some are surfaced over
// HTTP.
func TestErrorsCarryNoCredentialMaterial(t *testing.T) {
	const value = "sup3r-s3cret-value"
	const token = "an-access-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// A denial body that echoes back what it was sent — the reason the body is
		// never read into an error.
		_, _ = w.Write([]byte(`{"error":"forbidden","value":"` + value + `","token":"` + token + `"}`))
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL, staticTokens{token: token}).PutCredential(context.Background(),
		Address{Project: "p", Environment: "e", Key: "k"}, []byte(value), "")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), value)
	assert.NotContains(t, err.Error(), token)
}

func TestPutCredentialRequiresAnOutboundToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the request must not be sent without a token")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	err := newTestClient(t, srv.URL, staticTokens{err: errors.New("no control identity provisioned yet")}).
		PutCredential(context.Background(), Address{Project: "p", Environment: "e", Key: "k"}, []byte("v"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "https://secret.example.com", "the error names the audience the token was for")
}

func TestPutCredentialRefusesAnIncompleteAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("an unaddressable credential must never be sent")
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, staticTokens{token: "t"})
	require.Error(t, c.PutCredential(context.Background(), Address{Environment: "e", Key: "k"}, []byte("v"), ""))
	require.Error(t, c.PutCredential(context.Background(), Address{Project: "p", Key: "k"}, []byte("v"), ""))
	require.Error(t, c.PutCredential(context.Background(), Address{Project: "p", Environment: "e"}, []byte("v"), ""))
}

func TestCredentialsStoreReturnsAReferenceOnlyOnSuccess(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		ref, err := NewCredentials(newTestClient(t, srv.URL, staticTokens{token: "t"}), "default", "default").
			Store(context.Background(), "api", "db_password", []byte("v"))
		require.NoError(t, err)
		assert.Equal(t, "secret://default/default/api/db_password", ref)
	})

	// The reference must never come back from a write that did not happen: a spec
	// pointing at an address holding no secret is a workload that starts and then
	// cannot authenticate.
	t.Run("unavailable yields no reference", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		ref, err := NewCredentials(newTestClient(t, srv.URL, staticTokens{token: "t"}), "default", "default").
			Store(context.Background(), "api", "db_password", []byte("v"))
		require.ErrorIs(t, err, ErrUnavailable)
		assert.Empty(t, ref)
	})

	t.Run("no vault yields no reference", func(t *testing.T) {
		ref, err := NewCredentials(nil, "default", "default").
			Store(context.Background(), "api", "db_password", []byte("v"))
		require.ErrorIs(t, err, ErrNotConfigured)
		assert.Empty(t, ref)
	})
}

// A template or parameter name is operator input. A segment carrying a path
// separator would silently write the credential outside the address this policy
// chose.
func TestCredentialsRefuseAnEscapingSegment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("a credential with an unsafe address must never be sent")
	}))
	defer srv.Close()

	creds := NewCredentials(newTestClient(t, srv.URL, staticTokens{token: "t"}), "default", "default")
	for _, name := range []string{"../other", "api/nested", `api\nested`, "", "   "} {
		_, err := creds.Store(context.Background(), name, "db_password", []byte("v"))
		require.Error(t, err, "template %q must be refused", name)
	}
	_, err := creds.Store(context.Background(), "api", "../escape", []byte("v"))
	require.Error(t, err)
}
