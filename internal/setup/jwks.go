package setup

import "github.com/maintainerd/core/internal/steward"

// The control keypair helpers live in internal/steward: the setup-window applier
// here and the regular-surface applier (internal/authctrl) must mint, encode and
// re-derive keys identically, or a client provisioned during setup would not be
// verifiable against the same stored PEM afterwards. These thin wrappers keep
// the setup call sites reading the way they always have.

// generateControlKey mints Core's private_key_jwt signing key and returns the
// private PEM plus the public JWK Set Auth is given.
func generateControlKey() (privatePEM string, jwksJSON string, err error) {
	return steward.GenerateClientKey()
}

// jwksFromPrivatePEM re-derives the public JWK Set from a persisted private key
// (same modulus, same kid), which is what makes the keypair mint-once.
func jwksFromPrivatePEM(privatePEM string) (jwksJSON string, err error) {
	return steward.JWKSFromPrivatePEM(privatePEM)
}
