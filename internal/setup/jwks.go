package setup

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
)

// generateControlKey mints Core's private_key_jwt signing key. Auth's
// EnsureControlClient takes only the PUBLIC JWKS (RFC 7523), so Core keeps the
// private key and Auth never holds a credential that could impersonate Core.
// Returns the private key as PEM and the public JWK Set as JSON.
func generateControlKey() (privatePEM string, jwksJSON string, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("generate rsa key: %w", err)
	}

	privDER := x509.MarshalPKCS1PrivateKey(key)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privDER})

	pub := key.Public().(*rsa.PublicKey)
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	sum := sha256.Sum256(pub.N.Bytes())
	kid := fmt.Sprintf("%x", sum)[:16]

	jwks := map[string]any{
		"keys": []map[string]any{
			{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid, "n": n, "e": e},
		},
	}
	b, err := json.Marshal(jwks)
	if err != nil {
		return "", "", fmt.Errorf("marshal jwks: %w", err)
	}
	return string(privPEM), string(b), nil
}
