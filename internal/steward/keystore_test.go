package steward

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileKeyStoreRecordsOnceAndReuses(t *testing.T) {
	dir := t.TempDir()
	store := NewFileKeyStore(dir)

	pem, existed, err := store.PrivateKey("secret")
	require.NoError(t, err)
	assert.Empty(t, pem)
	assert.False(t, existed, "a missing key is the first-run signal, not an error")

	require.NoError(t, store.Record(context.Background(), "secret", "first-key"))

	// Create-exclusive: a repeated or concurrent run must never overwrite a live
	// credential.
	require.NoError(t, store.Record(context.Background(), "secret", "second-key"))

	pem, existed, err = store.PrivateKey("secret")
	require.NoError(t, err)
	assert.True(t, existed)
	assert.Equal(t, "first-key", pem)

	info, err := os.Stat(filepath.Join(dir, "secret.pem"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a private key must not be group- or world-readable")
}

func TestFileKeyStoreDiscardRollsBackAnOrphan(t *testing.T) {
	store := NewFileKeyStore(t.TempDir())
	require.NoError(t, store.Record(context.Background(), "secret", "key"))

	require.NoError(t, store.Discard("secret"))
	_, existed, err := store.PrivateKey("secret")
	require.NoError(t, err)
	assert.False(t, existed)

	// Idempotent: discarding a key that was never written is not a failure.
	require.NoError(t, store.Discard("secret"))
}

func TestFileKeyStoreRefusesUnsafeServiceNames(t *testing.T) {
	store := NewFileKeyStore(t.TempDir())
	// The service name becomes a filename, so traversal has to be refused before
	// it can write a key outside the configured directory.
	for _, name := range []string{"", "  ", "../escape", "nested/name", ".."} {
		_, _, err := store.PrivateKey(name)
		assert.Error(t, err, "service name %q", name)
		assert.Error(t, store.Record(context.Background(), name, "key"), "service name %q", name)
	}
}

func TestFileKeyStoreRequiresAConfiguredDirectory(t *testing.T) {
	store := NewFileKeyStore("")
	_, _, err := store.PrivateKey("secret")
	require.ErrorContains(t, err, "STEWARD_KEY_DIR")
}

func TestGenerateClientKeyRoundTripsToAStableJWKS(t *testing.T) {
	pem, jwks, err := GenerateClientKey()
	require.NoError(t, err)

	derived, err := JWKSFromPrivatePEM(pem)
	require.NoError(t, err)
	// The kid MUST be stable: auth looks the verification key up by kid, so a
	// re-derivation that produced a different one would break every assertion
	// signed with the same key.
	assert.JSONEq(t, jwks, derived)

	key, err := ParseRSAPrivateKey(pem)
	require.NoError(t, err)
	assert.Contains(t, jwks, KeyID(&key.PublicKey))
}
