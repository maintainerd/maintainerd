package setup

import (
	"os"
	"strings"
)

// Config drives the one-time orchestration Core runs against Auth (and the
// service registry it seeds afterwards). All values come from the environment;
// nothing is persisted here.
type Config struct {
	// Enabled turns on the on-boot orchestration (SETUP_ENABLED).
	Enabled bool

	// Auth gRPC SetupService connection.
	AuthAddr           string // AUTH_SETUP_ADDR (host:port of auth's gRPC)
	AuthToken          string // AUTH_SETUP_BOOTSTRAP_TOKEN (sent as x-setup-token)
	AuthCAFile         string // AUTH_SETUP_CA_FILE (optional; empty+no client cert = plaintext)
	AuthServerName     string // AUTH_SETUP_SERVER_NAME (TLS SNI; must match auth's gRPC cert SAN)
	AuthClientCertFile string // AUTH_SETUP_CLIENT_CERT_FILE (only if auth requires mTLS)
	AuthClientKeyFile  string // AUTH_SETUP_CLIENT_KEY_FILE

	// The system tenant + admin to provision in Auth.
	TenantName        string // SETUP_TENANT_NAME
	TenantDisplayName string // SETUP_TENANT_DISPLAY_NAME
	AdminUsername     string // SETUP_ADMIN_USERNAME
	AdminFullname     string // SETUP_ADMIN_FULLNAME
	AdminEmail        string // SETUP_ADMIN_EMAIL
	AdminPassword     string // SETUP_ADMIN_PASSWORD

	// Core's own control-plane identity registered in Auth.
	CoreServiceName     string   // fixed: maintainerd-core
	CoreAudience        string   // CORE_API_AUDIENCE (the API identifier tokens are minted for)
	ConsoleDomain       string   // CORE_CONSOLE_DOMAIN
	ConsoleRedirectURIs []string // CORE_CONSOLE_REDIRECT_URIS (comma-separated)

	// Endpoints recorded in Core's service registry for the system services.
	AuthEndpoint   string // = AuthAddr
	SecretEndpoint string // SECRET_ENDPOINT
	DockerEndpoint string // DOCKER_ENDPOINT

	// DeploymentMode (DEPLOYMENT_MODE: docker|kubernetes, default docker) is the
	// substrate this install reconciles onto. It is stamped into control_plane at
	// setup and is immutable afterwards — boot refuses to start when the env
	// disagrees with the stamp (see cmd/server). Validation happens in
	// internal/platform/config.Init; by the time this loads it is trustworthy.
	DeploymentMode string
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}

func envList(key, def string) []string {
	raw := env(key, def)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// LoadConfig reads the orchestration config from the environment.
func LoadConfig() Config {
	authAddr := env("AUTH_SETUP_ADDR", "")
	consoleDomain := env("CORE_CONSOLE_DOMAIN", "console.maintainerd.local")
	return Config{
		Enabled:            envBool("SETUP_ENABLED"),
		AuthAddr:           authAddr,
		AuthToken:          env("AUTH_SETUP_BOOTSTRAP_TOKEN", ""),
		AuthCAFile:         env("AUTH_SETUP_CA_FILE", ""),
		AuthServerName:     env("AUTH_SETUP_SERVER_NAME", ""),
		AuthClientCertFile: env("AUTH_SETUP_CLIENT_CERT_FILE", ""),
		AuthClientKeyFile:  env("AUTH_SETUP_CLIENT_KEY_FILE", ""),

		TenantName:        env("SETUP_TENANT_NAME", "maintainerd"),
		TenantDisplayName: env("SETUP_TENANT_DISPLAY_NAME", "Maintainerd"),
		AdminUsername:     env("SETUP_ADMIN_USERNAME", "admin"),
		AdminFullname:     env("SETUP_ADMIN_FULLNAME", "Maintainerd Admin"),
		AdminEmail:        env("SETUP_ADMIN_EMAIL", "admin@maintainerd.local"),
		AdminPassword:     env("SETUP_ADMIN_PASSWORD", ""),

		CoreServiceName: "maintainerd-core",
		// The API identifier (aud) tokens for Core are minted for — distinct from
		// the console origin.
		CoreAudience:        env("CORE_API_AUDIENCE", "https://core.maintainerd.local"),
		ConsoleDomain:       consoleDomain,
		ConsoleRedirectURIs: envList("CORE_CONSOLE_REDIRECT_URIS", "https://"+consoleDomain+"/auth/callback"),

		AuthEndpoint:   authAddr,
		SecretEndpoint: env("SECRET_ENDPOINT", ""),
		DockerEndpoint: env("DOCKER_ENDPOINT", ""),

		DeploymentMode: env("DEPLOYMENT_MODE", "docker"),
	}
}
