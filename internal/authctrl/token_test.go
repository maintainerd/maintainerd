package authctrl

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testIdentity(t *testing.T) Identity {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return Identity{
		ClientID:   "oauth-client-abc",
		TenantID:   "11111111-1111-1111-1111-111111111111",
		PrivateKey: key,
		KeyID:      "kid-1",
	}
}

// tokenServer stands in for auth's token endpoint. It records every assertion it
// is presented and hands back a distinct token per call so caching is provable.
type tokenServer struct {
	*httptest.Server
	assertions []string
	calls      int32
	expiresIn  int
	status     int
	body       string
}

func newTokenServer(t *testing.T) *tokenServer {
	t.Helper()
	ts := &tokenServer{expiresIn: 300, status: http.StatusOK}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		ts.assertions = append(ts.assertions, r.Form.Get("client_assertion"))
		n := atomic.AddInt32(&ts.calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(ts.status)
		if ts.body != "" {
			_, _ = w.Write([]byte(ts.body))
			return
		}
		_, _ = fmt.Fprintf(w, `{"access_token":"access-token-%d","token_type":"Bearer","expires_in":%d}`, n, ts.expiresIn)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestTokenSourceSignsRFC7523Assertion(t *testing.T) {
	srv := newTokenServer(t)
	identity := testIdentity(t)
	cfg := Config{TokenURL: srv.URL, Audience: "https://auth.local", Scopes: []string{"service:*", "api:*"}}
	ts := newTokenSource(cfg, identity, srv.Client())

	token, err := ts.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "access-token-1", token)
	require.Len(t, srv.assertions, 1)

	parsed, err := jwt.Parse(srv.assertions[0], func(*jwt.Token) (any, error) {
		return &identity.PrivateKey.PublicKey, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "RS256", parsed.Method.Alg())
	assert.Equal(t, "kid-1", parsed.Header["kid"])

	claims, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)
	// iss and sub are the OAuth client_id — auth compares BOTH against the
	// client's identifier column and rejects anything else, including the UUID.
	assert.Equal(t, identity.ClientID, claims["iss"])
	assert.Equal(t, identity.ClientID, claims["sub"])
	// aud is the token endpoint, which is what makes the assertion unreplayable
	// at any other relying party.
	assert.Equal(t, srv.URL, claims["aud"])
	assert.NotEmpty(t, claims["jti"])

	exp, err := claims.GetExpirationTime()
	require.NoError(t, err)
	iat, err := claims.GetIssuedAt()
	require.NoError(t, err)
	// Auth caps assertion lifetime at 5 minutes; staying well under it is what
	// keeps a captured assertion nearly useless.
	assert.LessOrEqual(t, exp.Sub(iat.Time), 5*time.Minute)
	assert.Equal(t, assertionLifetime, exp.Sub(iat.Time))
}

func TestTokenSourceSendsClientCredentialsGrantWithScopeAndAudience(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		form = r.Form
		_, _ = w.Write([]byte(`{"access_token":"t","expires_in":300}`))
	}))
	defer srv.Close()

	ts := newTokenSource(Config{
		TokenURL: srv.URL, Audience: "https://auth.local", Scopes: []string{"service:*", "policy:*"},
	}, testIdentity(t), srv.Client())
	_, err := ts.Token(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "client_credentials", form.Get("grant_type"))
	assert.Equal(t, "urn:ietf:params:oauth:client-assertion-type:jwt-bearer", form.Get("client_assertion_type"))
	assert.Equal(t, "https://auth.local", form.Get("audience"))
	assert.Equal(t, "service:* policy:*", form.Get("scope"))
}

func TestTokenSourceCachesUntilRefreshSkew(t *testing.T) {
	srv := newTokenServer(t)
	srv.expiresIn = 300
	ts := newTokenSource(Config{TokenURL: srv.URL, RefreshSkew: 60 * time.Second}, testIdentity(t), srv.Client())

	base := time.Now()
	ts.now = func() time.Time { return base }

	first, err := ts.Token(context.Background())
	require.NoError(t, err)
	second, err := ts.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.EqualValues(t, 1, atomic.LoadInt32(&srv.calls), "a cached token must not hit the token endpoint again")

	// Still outside the skew window (300s life, 60s skew → valid for 240s).
	ts.now = func() time.Time { return base.Add(239 * time.Second) }
	third, err := ts.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first, third)
	assert.EqualValues(t, 1, atomic.LoadInt32(&srv.calls))

	// Inside the skew window: refresh BEFORE expiry, so a reconcile pass never
	// rides a token that dies mid-flight.
	ts.now = func() time.Time { return base.Add(241 * time.Second) }
	fourth, err := ts.Token(context.Background())
	require.NoError(t, err)
	assert.NotEqual(t, first, fourth)
	assert.EqualValues(t, 2, atomic.LoadInt32(&srv.calls))
}

func TestTokenSourceHalvesSkewForShortLivedTokens(t *testing.T) {
	srv := newTokenServer(t)
	srv.expiresIn = 30 // shorter than the 60s skew
	ts := newTokenSource(Config{TokenURL: srv.URL, RefreshSkew: 60 * time.Second}, testIdentity(t), srv.Client())

	base := time.Now()
	ts.now = func() time.Time { return base }
	first, err := ts.Token(context.Background())
	require.NoError(t, err)

	// Skew halves to 15s, so the token stays cached for 15s rather than being
	// considered stale the instant it is minted.
	ts.now = func() time.Time { return base.Add(14 * time.Second) }
	cached, err := ts.Token(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first, cached)

	ts.now = func() time.Time { return base.Add(16 * time.Second) }
	refreshed, err := ts.Token(context.Background())
	require.NoError(t, err)
	assert.NotEqual(t, first, refreshed)
}

func TestTokenSourceErrorsNeverLeakCredentials(t *testing.T) {
	srv := newTokenServer(t)
	srv.status = http.StatusUnauthorized
	srv.body = `{"error":"invalid_client","error_description":"assertion signature invalid","access_token":"leaked-token-value"}`

	ts := newTokenSource(Config{TokenURL: srv.URL}, testIdentity(t), srv.Client())
	_, err := ts.Token(context.Background())
	require.Error(t, err)

	// The OAuth failure reason is quotable — it is what an operator needs.
	assert.Contains(t, err.Error(), "invalid_client")
	assert.Contains(t, err.Error(), "assertion signature invalid")

	// The assertion and any token material are NOT. Both are bearer credentials
	// and this error is logged.
	require.Len(t, srv.assertions, 1)
	assert.NotContains(t, err.Error(), srv.assertions[0])
	assert.NotContains(t, err.Error(), "leaked-token-value")
	// Not even a fragment: a JWT segment is enough to matter.
	for _, part := range strings.Split(srv.assertions[0], ".") {
		if len(part) > 16 {
			assert.NotContains(t, err.Error(), part)
		}
	}
}

func TestTokenSourceRequiresIdentityAndTokenURL(t *testing.T) {
	t.Run("no token url", func(t *testing.T) {
		ts := newTokenSource(Config{}, testIdentity(t), nil)
		_, err := ts.Token(context.Background())
		require.ErrorContains(t, err, "AUTH_TOKEN_URL")
	})

	t.Run("no identity", func(t *testing.T) {
		ts := newTokenSource(Config{TokenURL: "https://auth.local/token"}, Identity{}, nil)
		_, err := ts.Token(context.Background())
		require.ErrorIs(t, err, ErrNoControlIdentity)
	})
}
