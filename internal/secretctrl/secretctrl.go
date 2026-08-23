// Package secretctrl is core's outbound half of the core→secret dependency.
//
// maintainerd-secret is a HARD DEPENDENCY of maintainerd-core, not an optional
// integration. Core provisions services, and a provisioned service's generated
// credentials belong in the vault — the AWS ECS / Elastic Beanstalk model, where
// managing a workload's environment is a first-class platform feature. A core
// that cannot reach secret cannot provision, which is why the boot gate
// (cmd/server) refuses to start without it.
//
// THE ONE DISTINCTION THIS PACKAGE EXISTS TO KEEP:
//
//	At BOOT, unreachable is fatal — refuse to start.
//	AFTER boot, unreachable is a DEGRADATION — /readyz reports it and the process
//	keeps running, because existing workloads are still running and the console
//	still has to answer. A vault blip must not become a control-plane outage.
//	At PROVISION time, unreachable FAILS CLOSED — the provision errors and the
//	credential is never written anywhere else. Falling back to a plain
//	environment variable is how a secret ends up in a container's environment
//	while the operator believes it is in the vault.
//
// Nothing here logs a credential. Errors name the dependency, the operation and
// the HTTP status; never a token, a value, or a response body.
package secretctrl

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotConfigured means SECRET_BASE_URL is unset, so this process has no vault
// to write to. Distinct from ErrUnavailable because the fix is different in kind:
// a missing configuration is an install problem an operator resolves once,
// whereas an unreachable vault is an outage that resolves itself. Outside
// development config.Init refuses this state at boot, so it is only reachable in
// development.
var ErrNotConfigured = errors.New("secretctrl: SECRET_BASE_URL is not configured")

// ErrUnavailable means maintainerd-secret did not answer, or answered that it is
// not ready. Callers on a provisioning path MUST surface it and abort; there is
// no fallback destination for a credential.
var ErrUnavailable = errors.New("secretctrl: maintainerd-secret is unavailable")

// TokenSource mints the bearer token core presents to secret's guarded API.
// *authctrl.OutboundTokens satisfies it — core's existing RFC 7523
// private_key_jwt identity, asking auth for a token whose audience is secret's
// resource API.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Config is what core needs in order to call secret.
type Config struct {
	// BaseURL is secret's HTTP origin (SECRET_BASE_URL), no trailing slash.
	BaseURL string

	// Audience is secret's resource-API identifier (SECRET_API_AUDIENCE) — the
	// `aud` core asks auth to mint its outbound token for. It is carried here
	// rather than resolved from the catalog because the catalog's copy is the
	// INTENT core registers in auth, and this one is what this process presents.
	Audience string

	// Timeout bounds every call, probe and write alike
	// (DEPENDENCY_PROBE_TIMEOUT).
	Timeout time.Duration

	// Tenant selects which tenant a write addresses (secret's
	// X-Maintainerd-Tenant selector). Empty means secret's default tenant, which
	// is the correct answer for core's own system-tier provisioning.
	Tenant string
}

// Client is core's handle on maintainerd-secret's REST API.
//
// A nil *Client is a valid, fully unavailable client: every method returns
// ErrNotConfigured. That is deliberate — development runs with no vault, and the
// call sites must take the same fail-closed branch there as they do when a
// configured vault is down, rather than skipping the check.
type Client struct {
	cfg    Config
	tokens TokenSource
	http   *http.Client
}

// New builds a client, or nil when no vault is configured (development only —
// see ErrNotConfigured).
func New(cfg Config, tokens TokenSource) *Client {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	return &Client{cfg: cfg, tokens: tokens, http: &http.Client{Timeout: cfg.Timeout}}
}

// Configured reports whether this process has a vault to talk to.
func (c *Client) Configured() bool { return c != nil }

// Probe reports whether secret can serve right now.
//
// IT READS /readyz, NOT /healthz. Liveness only says the process is scheduling
// goroutines; readiness is secret's own answer to "can I serve a request", which
// is the question core is actually asking — a vault that is up but cannot reach
// its store or verify a token is of no use to a provision. Unauthenticated by
// design on secret's side, so this works before core holds any credential.
func (c *Client) Probe(ctx context.Context) error {
	if c == nil {
		return ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/readyz", nil)
	if err != nil {
		return fmt.Errorf("%w: build readiness request: %w", ErrUnavailable, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error stringifies the URL only. The URL is operator configuration,
		// not a credential.
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drained so the connection is reusable; the body is secret's own probe
	// payload and is deliberately not read into an error.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: readiness probe answered %d", ErrUnavailable, resp.StatusCode)
	}
	return nil
}

// Address is where a credential lives in the vault: secret's
// project/environment/folder/key coordinates.
type Address struct {
	Project     string
	Environment string
	FolderPath  string
	Key         string
}

// Ref renders the address as the inert reference a workload spec carries in
// place of the value. Specs are persisted and shipped over the wire, so a name
// is safe where a value is a leak (the rule kit states on
// WorkloadSpec.RegistryCredentialRef).
func (a Address) Ref() string {
	path := strings.Trim(a.FolderPath, "/")
	if path != "" {
		path += "/"
	}
	return "secret://" + a.Project + "/" + a.Environment + "/" + path + a.Key
}

// PutCredential writes a generated credential into the vault.
//
// FAIL CLOSED, ALWAYS. Every failure path returns an error and writes the value
// nowhere else. There is deliberately no "store it locally and retry later" and
// no "return it to the caller so it can be inlined" — either the vault holds the
// credential or the provision did not happen.
//
// value is the raw plaintext. It is base64-encoded for transport (secret's API
// takes bytes, not a string) and never appears in an error or a log line here.
func (c *Client) PutCredential(ctx context.Context, addr Address, value []byte, description string) error {
	if c == nil {
		return ErrNotConfigured
	}
	if addr.Project == "" || addr.Environment == "" || addr.Key == "" {
		return fmt.Errorf("secretctrl: incomplete secret address (project, environment and key are required)")
	}

	body, err := json.Marshal(map[string]any{
		"project":     addr.Project,
		"environment": addr.Environment,
		"folder_path": addr.FolderPath,
		"key":         addr.Key,
		"value":       base64.StdEncoding.EncodeToString(value),
		"value_type":  "string",
		"description": description,
		// Core provisions into folders it owns; requiring an operator to pre-create
		// the hierarchy would make the first provision of every service fail.
		"create_folders": true,
	})
	if err != nil {
		return fmt.Errorf("secretctrl: encode secret write: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/api/v1/secrets", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("secretctrl: build secret write: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.cfg.Tenant != "" {
		req.Header.Set(tenantHeader, c.cfg.Tenant)
	}
	if c.tokens != nil {
		token, terr := c.tokens.Token(ctx)
		if terr != nil {
			// The token error is wrapped, not restated: authctrl already guarantees
			// its errors carry no assertion, form or token material.
			return fmt.Errorf("secretctrl: no outbound token for %s: %w", c.cfg.Audience, terr)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	switch {
	case resp.StatusCode == http.StatusOK, resp.StatusCode == http.StatusCreated:
		return nil
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		// Secret is there but cannot serve — the same class as unreachable, so the
		// same sentinel: a caller deciding whether to abort a provision does not
		// care which.
		return fmt.Errorf("%w: secret write answered %d", ErrUnavailable, resp.StatusCode)
	default:
		// A 4xx is core's fault (wrong audience, missing grant, bad address) and is
		// NOT ErrUnavailable — retrying will not fix it. The status is quotable; the
		// response body is not, because a denial can echo the address.
		return fmt.Errorf("secretctrl: secret refused the write with status %d", resp.StatusCode)
	}
}

// tenantHeader is secret's tenant SELECTOR (its httpapi.TenantHeader). Core
// cannot import that constant — secret is optional and independently released —
// so the two are kept in step by review, like the permission catalog in
// internal/steward.
const tenantHeader = "X-Maintainerd-Tenant"
