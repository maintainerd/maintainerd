package authctrl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/maintainerd/core/gen/maintainerd/auth/v1"
)

// Client is core's authenticated handle on auth's regular management surface.
//
// It is built lazily: auth may not be reachable — or core may not even be
// provisioned — when the process starts, so nothing here dials or loads an
// identity until the first Connect. Connect is idempotent and safe to call from
// the boot retry loop and an operator's REST request at the same time.
type Client struct {
	cfg   Config
	store ControlPlaneStore

	mu       sync.Mutex
	conn     *grpc.ClientConn
	identity Identity
	tokens   *tokenSource

	Services    authv1.ServiceServiceClient
	APIs        authv1.APIServiceClient
	Permissions authv1.PermissionServiceClient
	Roles       authv1.RoleServiceClient
	Policies    authv1.PolicyServiceClient
	Clients     authv1.ClientServiceClient
}

func New(cfg Config, store ControlPlaneStore) *Client {
	return &Client{cfg: cfg, store: store}
}

// Connect loads the persisted control identity and dials auth. It returns
// ErrNoControlIdentity — unwrapped, so callers can match it — when setup has not
// yet issued core its credential.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return nil
	}

	identity, err := LoadIdentity(ctx, c.store)
	if err != nil {
		return err
	}
	if c.cfg.GRPCAddr == "" {
		return fmt.Errorf("authctrl: no auth gRPC address configured (set AUTH_CTRL_GRPC_ADDR or AUTH_SETUP_ADDR)")
	}

	transport, err := c.transportCreds()
	if err != nil {
		return err
	}
	tokens := newTokenSource(c.cfg, identity, nil)

	conn, err := grpc.NewClient(c.cfg.GRPCAddr,
		grpc.WithTransportCredentials(transport),
		grpc.WithPerRPCCredentials(bearerCredentials{
			tokens: tokens,
			// A bearer token on a plaintext connection is a credential handed to
			// the network. gRPC refuses it unless we explicitly say the transport
			// is insecure, and we only say that when NO TLS material is configured
			// at all — the same development-only escape hatch the setup dialer has.
			secure: !c.cfg.plaintext(),
		}),
	)
	if err != nil {
		return fmt.Errorf("authctrl: dial auth: %w", err)
	}

	c.conn = conn
	c.identity = identity
	c.tokens = tokens
	c.Services = authv1.NewServiceServiceClient(conn)
	c.APIs = authv1.NewAPIServiceClient(conn)
	c.Permissions = authv1.NewPermissionServiceClient(conn)
	c.Roles = authv1.NewRoleServiceClient(conn)
	c.Policies = authv1.NewPolicyServiceClient(conn)
	c.Clients = authv1.NewClientServiceClient(conn)
	return nil
}

// TenantID is auth's system-tenant UUID, which every management RPC takes.
// Empty until Connect succeeds.
func (c *Client) TenantID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.identity.TenantID
}

// Close releases the connection. Safe to call on a Client that never connected.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// transportCreds mirrors the setup dialer: plaintext only when no TLS material
// is configured at all, otherwise TLS with the configured CA and — when auth
// requires mTLS — core's client certificate.
func (c *Client) transportCreds() (credentials.TransportCredentials, error) {
	if c.cfg.plaintext() {
		return insecure.NewCredentials(), nil
	}
	tlsCfg := &tls.Config{ServerName: c.cfg.ServerName, MinVersion: tls.VersionTLS12}
	if c.cfg.CAFile != "" {
		caPEM, err := os.ReadFile(c.cfg.CAFile) // #nosec G304 -- operator-configured trust anchor path
		if err != nil {
			return nil, fmt.Errorf("authctrl: read auth CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("authctrl: auth CA file %q holds no valid certificate", c.cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	if c.cfg.ClientCertFile != "" && c.cfg.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.cfg.ClientCertFile, c.cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("authctrl: load auth client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return credentials.NewTLS(tlsCfg), nil
}

// bearerCredentials attaches the control access token to every RPC.
type bearerCredentials struct {
	tokens *tokenSource
	secure bool
}

func (b bearerCredentials) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	token, err := b.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]string{"authorization": "Bearer " + token}, nil
}

func (b bearerCredentials) RequireTransportSecurity() bool { return b.secure }
