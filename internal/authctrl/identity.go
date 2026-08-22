package authctrl

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/maintainerd/core/internal/steward"
	"github.com/maintainerd/core/internal/storage"
)

// ErrNoControlIdentity means core has not (yet) been issued the control-plane
// identity this package authenticates with: setup has not run, did not
// complete, or completed before the control client existed.
//
// It is a distinct sentinel and not a generic failure because the correct
// response is different in kind. A missing identity is an ORDERING problem —
// setup simply has not happened yet — so the boot loop must log it and keep
// retrying rather than treat it as a broken install, and the REST surface must
// answer "not provisioned" rather than "auth is down". Crashing on it would put
// a fresh install into a boot loop it can never escape, since setup is what
// creates the very thing that is missing.
var ErrNoControlIdentity = errors.New("authctrl: no control-plane identity is provisioned yet")

// ControlPlaneStore is the slice of storage this package reads. Narrowed to an
// interface so the identity rules are testable without a database;
// *storage.Queries satisfies it.
type ControlPlaneStore interface {
	GetControlPlane(ctx context.Context) (storage.ControlPlane, error)
}

// Identity is core's m2m control-client credential, loaded from the
// control_plane singleton row.
type Identity struct {
	// ClientID is the OAuth client_id (auth's `identifier` column), NOT the
	// client UUID. It is what the client assertion's iss and sub must equal —
	// auth compares both against that column and rejects the UUID.
	ClientID string

	// TenantID is auth's system-tenant UUID. Auth's management RPCs are all
	// tenant-scoped and take it explicitly.
	TenantID string

	// PrivateKey signs the assertion. Auth holds only the matching public JWKS.
	PrivateKey *rsa.PrivateKey

	// KeyID is the JWK kid of that public key, sent in the assertion header so
	// auth can select the right key from the set.
	KeyID string
}

// LoadIdentity reads and validates the persisted control identity.
//
// It fails closed: a partially provisioned row (a key but no client id, a client
// id but no tenant) yields ErrNoControlIdentity rather than a half-usable
// credential that would fail later, deeper, and less legibly.
func LoadIdentity(ctx context.Context, store ControlPlaneStore) (Identity, error) {
	if store == nil {
		return Identity{}, ErrNoControlIdentity
	}
	row, err := store.GetControlPlane(ctx)
	if err != nil {
		// A missing row is the fresh-install case, not a storage fault. Any other
		// error is a real failure and must not be disguised as "not set up".
		if isNoRows(err) {
			return Identity{}, ErrNoControlIdentity
		}
		return Identity{}, fmt.Errorf("authctrl: read control plane: %w", err)
	}

	pem := strings.TrimSpace(row.ControlPrivateKeyPem)
	if pem == "" {
		return Identity{}, ErrNoControlIdentity
	}

	var res struct {
		ControlClientID      string `json:"control_client_id"`
		ControlOAuthClientID string `json:"control_oauth_client_id"`
		AuthTenantID         string `json:"auth_tenant_id"`
	}
	if len(row.Data) > 0 {
		if err := json.Unmarshal(row.Data, &res); err != nil {
			return Identity{}, fmt.Errorf("authctrl: decode control plane data: %w", err)
		}
	}

	// The OAuth client_id is the assertion subject. ControlClientID (the UUID) is
	// never a valid substitute, so there is no fallback — an install missing the
	// oauth id is not provisioned for this path.
	clientID := strings.TrimSpace(res.ControlOAuthClientID)
	tenantID := strings.TrimSpace(firstNonEmpty(res.AuthTenantID, uuidOrEmpty(row)))
	if clientID == "" || tenantID == "" {
		return Identity{}, ErrNoControlIdentity
	}

	key, err := steward.ParseRSAPrivateKey(pem)
	if err != nil {
		// A stored-but-unusable key is corruption, not absence: surface it rather
		// than let the boot loop retry forever against a key that will never work.
		return Identity{}, fmt.Errorf("authctrl: stored control key is unusable: %w", err)
	}

	return Identity{
		ClientID:   clientID,
		TenantID:   tenantID,
		PrivateKey: key,
		KeyID:      steward.KeyID(&key.PublicKey),
	}, nil
}

// isNoRows reports the fresh-install case: the control_plane singleton has never
// been written. Matched by pgx's sentinel.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// uuidOrEmpty falls back to the dedicated auth_tenant_uuid column when the JSON
// blob does not carry the tenant (a setup run that skipped CreateTenant records
// no auth_tenant_id in data, but the column is still populated).
func uuidOrEmpty(row storage.ControlPlane) string {
	if !row.AuthTenantUuid.Valid {
		return ""
	}
	return uuid.UUID(row.AuthTenantUuid.Bytes).String()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
