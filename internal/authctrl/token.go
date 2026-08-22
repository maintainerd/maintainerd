package authctrl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// assertionLifetime is how long a client assertion is valid. Auth caps the
// lifetime it will accept at 5 minutes (RFC 7523 §3); staying well under that
// leaves room for clock skew in both directions and keeps a captured assertion
// useless almost immediately.
const assertionLifetime = 2 * time.Minute

// tokenSource mints and caches core's control access token.
//
// It is a small local implementation rather than the SDK's PrivateKeyJWT for
// three reasons the control path depends on: the refresh skew has to be wide
// enough for a whole reconcile pass (see DefaultRefreshSkew), a failure has to
// be distinguishable and provably free of credential material (see the
// redaction rules below), and the assertion's claims have to be assertable in a
// test — the SDK's are unexported.
//
// Safe for concurrent use.
type tokenSource struct {
	cfg      Config
	identity Identity
	http     *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time

	// now is injectable so expiry/refresh behaviour is testable without sleeping.
	now func() time.Time
}

func newTokenSource(cfg Config, identity Identity, hc *http.Client) *tokenSource {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.RefreshSkew <= 0 {
		cfg.RefreshSkew = DefaultRefreshSkew
	}
	return &tokenSource{cfg: cfg, identity: identity, http: hc, now: time.Now}
}

// Token returns a cached access token, minting a fresh one when none is held or
// the cached one is inside the refresh skew of expiry.
func (t *tokenSource) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != "" && t.now().Before(t.exp) {
		return t.token, nil
	}
	token, ttl, err := t.mint(ctx)
	if err != nil {
		return "", err
	}
	skew := t.cfg.RefreshSkew
	if ttl <= skew {
		// A token shorter than the skew still has to be usable; halving keeps a
		// margin without making the cache useless.
		skew = ttl / 2
	}
	t.token = token
	t.exp = t.now().Add(ttl - skew)
	return token, nil
}

// mint performs the RFC 7523 client-credentials exchange.
func (t *tokenSource) mint(ctx context.Context) (string, time.Duration, error) {
	if strings.TrimSpace(t.cfg.TokenURL) == "" {
		return "", 0, fmt.Errorf("authctrl: AUTH_TOKEN_URL is not set")
	}
	assertion, err := t.signAssertion()
	if err != nil {
		return "", 0, err
	}
	form := url.Values{
		"grant_type":            {"client_credentials"},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {assertion},
	}
	if t.cfg.Audience != "" {
		form.Set("audience", t.cfg.Audience)
	}
	if len(t.cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(t.cfg.Scopes, " "))
	}
	return t.postToken(ctx, form)
}

// signAssertion builds the RFC 7523 client assertion.
//
// iss and sub are the OAuth client_id — auth compares BOTH against the client's
// `identifier` column — and aud is the token endpoint, which is what makes the
// assertion unreplayable at any other relying party.
func (t *tokenSource) signAssertion() (string, error) {
	if t.identity.PrivateKey == nil || t.identity.ClientID == "" {
		return "", ErrNoControlIdentity
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("authctrl: generate assertion jti: %w", err)
	}
	now := t.now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": t.identity.ClientID,
		"sub": t.identity.ClientID,
		"aud": t.cfg.TokenURL,
		"jti": hex.EncodeToString(raw),
		"iat": now.Unix(),
		"exp": now.Add(assertionLifetime).Unix(),
	})
	if t.identity.KeyID != "" {
		tok.Header["kid"] = t.identity.KeyID
	}
	signed, err := tok.SignedString(t.identity.PrivateKey)
	if err != nil {
		// Deliberately not wrapped with the assertion or key: a signing failure is
		// about the key's shape, and the error travels into logs.
		return "", fmt.Errorf("authctrl: sign client assertion: %w", err)
	}
	return signed, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// postToken exchanges the assertion for an access token.
//
// REDACTION RULE: no error returned from here may carry the assertion, the
// request form, the access token, or the raw response body. Those are bearer
// credentials, and every error on this path is logged and some are surfaced over
// HTTP. Only the OAuth error code/description (which auth authors, and which
// names a failure mode) and the status code are quotable.
func (t *tokenSource) postToken(ctx context.Context, form url.Values) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("authctrl: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		// url.Error stringifies the request URL only, never the body.
		return "", 0, fmt.Errorf("authctrl: token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var tr tokenResponse
	// Bounded: an unexpected upstream should not let core read an arbitrary body
	// into memory.
	body := io.LimitReader(resp.Body, 1<<20)
	decodeErr := json.NewDecoder(body).Decode(&tr)

	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		reason := strings.TrimSpace(tr.Error)
		if tr.ErrorDesc != "" {
			reason = strings.TrimSpace(reason + ": " + tr.ErrorDesc)
		}
		if reason == "" {
			reason = fmt.Sprintf("status %d", resp.StatusCode)
		}
		return "", 0, fmt.Errorf("authctrl: token endpoint rejected the control client (%s)", reason)
	}
	if decodeErr != nil {
		return "", 0, fmt.Errorf("authctrl: decode token response: %w", decodeErr)
	}

	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl <= 0 {
		// Conservative default when the server omits expires_in: re-mint often
		// rather than cache a token of unknown lifetime.
		ttl = 5 * time.Minute
	}
	return tr.AccessToken, ttl, nil
}
