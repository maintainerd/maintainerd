package authctrl

import (
	"os"
	"strings"
	"time"
)

// Config is the post-setup control path's connection settings.
//
// Every transport value defaults to the setup path's equivalent (AUTH_SETUP_*):
// it is the same auth, on the same listener, presenting the same certificate, so
// requiring an operator to restate all of it would only create a way to get it
// wrong. The AUTH_CTRL_* overrides exist for the deployments where the control
// path really does differ — a separate management listener, a different client
// certificate for the m2m identity.
type Config struct {
	// TokenURL (AUTH_TOKEN_URL) is auth's OAuth2 token endpoint. Required: with
	// no token endpoint there is no way to exchange the client assertion, so the
	// control path is inert.
	TokenURL string

	// GRPCAddr (AUTH_CTRL_GRPC_ADDR, default AUTH_SETUP_ADDR) is auth's gRPC
	// management listener.
	GRPCAddr string

	// TLS material for that listener (AUTH_CTRL_*, defaulting to AUTH_SETUP_*).
	// Plaintext only when NO material is configured at all, matching the setup
	// dialer — see Client.dial.
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
	ServerName     string

	// Audience (AUTH_CTRL_AUDIENCE) is the API the minted access token is
	// requested for; Scopes (AUTH_CTRL_SCOPES, comma or space separated) narrows
	// it further. Both optional — auth falls back to the client's own grants.
	Audience string
	Scopes   []string

	// RefreshSkew is how far before expiry a cached token is replaced, so a call
	// never rides a token that expires mid-flight.
	RefreshSkew time.Duration
}

// DefaultRefreshSkew is deliberately larger than the SDK's 30s: these calls are
// a reconcile loop that can issue dozens of RPCs in a row, and a token that
// expires part-way through leaves auth half-converged.
const DefaultRefreshSkew = 60 * time.Second

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envList splits on both commas and whitespace so either OAuth's space-separated
// scope form or the comma-separated form this repo uses elsewhere works.
func envList(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LoadConfig reads the control-path config from the environment.
func LoadConfig() Config {
	return Config{
		TokenURL:       env("AUTH_TOKEN_URL", ""),
		GRPCAddr:       env("AUTH_CTRL_GRPC_ADDR", env("AUTH_SETUP_ADDR", "")),
		CAFile:         env("AUTH_CTRL_CA_FILE", env("AUTH_SETUP_CA_FILE", "")),
		ClientCertFile: env("AUTH_CTRL_CLIENT_CERT_FILE", env("AUTH_SETUP_CLIENT_CERT_FILE", "")),
		ClientKeyFile:  env("AUTH_CTRL_CLIENT_KEY_FILE", env("AUTH_SETUP_CLIENT_KEY_FILE", "")),
		ServerName:     env("AUTH_CTRL_SERVER_NAME", env("AUTH_SETUP_SERVER_NAME", "")),
		Audience:       env("AUTH_CTRL_AUDIENCE", ""),
		Scopes:         envList("AUTH_CTRL_SCOPES"),
		RefreshSkew:    DefaultRefreshSkew,
	}
}

// plaintext reports whether the gRPC dial has no TLS material at all. Mirrors
// the setup dialer's rule so a development stack that works for setup keeps
// working for the control path.
func (c Config) plaintext() bool {
	return c.CAFile == "" && c.ClientCertFile == ""
}
