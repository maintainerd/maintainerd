package authctrl

import (
	"context"
	"sync"
)

// OutboundTokens mints core's control-identity access tokens for a relying party
// OTHER than auth's own management API — today, maintainerd-secret.
//
// IT EXISTS SO CORE HAS EXACTLY ONE OUTBOUND CREDENTIAL MECHANISM. Core already
// authenticates to auth with RFC 7523 private_key_jwt against the keypair setup
// issued it (see Identity); talking to secret is the same client asking the same
// token endpoint for a token with a different `audience`. A second mechanism — a
// client secret in core's environment, a shared static token — would be a second
// credential to rotate, a second way to leak, and a second thing that can
// disagree with what auth believes core is.
//
// It is lazy for the reason Client.Connect is: at wiring time setup may not have
// issued core its identity yet, so nothing is loaded until the first Token call.
// Until then Token returns ErrNoControlIdentity, which callers must treat as
// "not provisioned yet", not as "auth is down".
type OutboundTokens struct {
	cfg   Config
	store ControlPlaneStore

	mu  sync.Mutex
	src *tokenSource
}

// NewOutboundTokens builds a token source for one audience and scope set.
//
// The transport settings come from the shared control-path Config (token
// endpoint, refresh skew); only the audience and scopes are per-relying-party,
// because those are what auth mints the token FOR.
func NewOutboundTokens(cfg Config, store ControlPlaneStore, audience string, scopes []string) *OutboundTokens {
	cfg.Audience = audience
	cfg.Scopes = scopes
	return &OutboundTokens{cfg: cfg, store: store}
}

// Token returns a cached access token for the configured audience, minting one
// on first use and whenever the cached one nears expiry.
func (o *OutboundTokens) Token(ctx context.Context) (string, error) {
	src, err := o.source(ctx)
	if err != nil {
		return "", err
	}
	return src.Token(ctx)
}

func (o *OutboundTokens) source(ctx context.Context) (*tokenSource, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.src != nil {
		return o.src, nil
	}
	identity, err := LoadIdentity(ctx, o.store)
	if err != nil {
		return nil, err
	}
	// nil client: newTokenSource installs a bounded one. http.DefaultClient has no
	// timeout, and a token endpoint that accepts the connection and never answers
	// would hang whichever provision is waiting on this credential.
	o.src = newTokenSource(o.cfg, identity, nil)
	return o.src, nil
}
