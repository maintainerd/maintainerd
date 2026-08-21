package setup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWKSFromPrivatePEMRoundTrip(t *testing.T) {
	pem, jwks, err := generateControlKey()
	require.NoError(t, err)

	derived, err := jwksFromPrivatePEM(pem)
	require.NoError(t, err)
	// Byte-for-byte JSON equality matters less than semantic equality, but the
	// kid derivation MUST be stable: Auth looks the key up by kid, so a re-run
	// that produced a different kid for the same key would break token signing.
	assert.JSONEq(t, jwks, derived)
}

func TestJWKSFromPrivatePEMRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not a pem", "-----BEGIN RSA PRIVATE KEY-----\nZm9v\n-----END RSA PRIVATE KEY-----"} {
		_, err := jwksFromPrivatePEM(bad)
		assert.Error(t, err, "input %q", bad)
	}
}
