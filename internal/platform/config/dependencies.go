package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// EnvDevelopment is the one APP_ENV value that relaxes the dependency gates.
const EnvDevelopment = "development"

// IsDevelopment reports the relaxed posture. APP_ENV defaults to "production"
// (see Init), so this is false unless an operator opted in.
func IsDevelopment() bool { return AppEnv == EnvDevelopment }

// validateDependencies enforces that core's two HARD dependencies — auth and
// secret — are CONFIGURED. Reachability is a separate, later check (cmd/server),
// because it needs a context and a network.
//
// ONE ERROR NAMING EVERY MISSING VARIABLE, which is the whole point of doing
// this here instead of at each use site. A message that reports them one boot at
// a time turns a five-minute setup into five restarts — the same argument the
// secret service's standaloneMissing/kmsMissing make.
//
// Development WARNS instead. Core's other development affordances (dev-open
// guards, relaxed TLS) all work that way, and a contributor running core alone
// must not need a vault to see a compile succeed.
func validateDependencies() error {
	missing := dependencyMissing()
	if IsDevelopment() {
		if len(missing) > 0 {
			slog.Warn("config: auth/secret configuration is incomplete — permitted in "+EnvDevelopment+" only; "+
				"the guards open and provisioning that needs secret material will refuse",
				"missing", strings.Join(missing, ", "))
		}
		return nil
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"config: maintainerd-auth and maintainerd-secret are hard dependencies of maintainerd-core "+
				"and this install is missing %s. Auth issues the tokens core verifies and mints the ones it "+
				"presents outbound; secret holds the credentials core generates when it provisions a service. "+
				"Set APP_ENV=%s to run without them",
			strings.Join(missing, ", "), EnvDevelopment)
	}
	// Transport, checked only once the value exists so a missing variable is
	// reported by the aggregate above rather than as a malformed URL.
	return validateSecretTransport()
}

// dependencyMissing lists the auth/secret variables this install needs and does
// not have, in the order an operator would set them.
func dependencyMissing() []string {
	var missing []string
	if AuthJWKSURL == "" {
		missing = append(missing, "AUTH_JWKS_URL")
	}
	if AuthIssuer == "" {
		missing = append(missing, "AUTH_ISSUER")
	}
	if AuthAudience == "" {
		missing = append(missing, "AUTH_AUDIENCE")
	}
	if AuthTokenURL == "" {
		missing = append(missing, "AUTH_TOKEN_URL")
	}
	if SecretBaseURL == "" {
		missing = append(missing, "SECRET_BASE_URL")
	}
	return missing
}

// validateSecretTransport refuses a secret origin core cannot talk to safely.
//
// Outside development the scheme must be https: every request core sends there
// carries a bearer token that can read every secret core has written, and on a
// plaintext hop that token belongs to the network. Development is exempt because
// the local stack reaches secret over the compose network by container name.
func validateSecretTransport() error {
	u, err := url.Parse(SecretBaseURL)
	if err != nil {
		return fmt.Errorf("SECRET_BASE_URL is not a valid URL: %w", err)
	}
	if u.Host == "" {
		return fmt.Errorf("SECRET_BASE_URL must be an absolute URL with a scheme and host (got %q)", SecretBaseURL)
	}
	if !IsDevelopment() && !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf(
			"SECRET_BASE_URL must use https outside %s (got scheme %q) — every request core sends there "+
				"carries a bearer token for the vault", EnvDevelopment, u.Scheme)
	}
	return nil
}
