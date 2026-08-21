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

	jwks, err := jwksFromPublicKey(key.Public().(*rsa.PublicKey))
	if err != nil {
		return "", "", err
	}
	return string(privPEM), jwks, nil
}

// jwksFromPrivatePEM re-derives the public JWK Set from a persisted private
// key. This is what makes the control keypair mint-once: a setup re-run reuses
// the stored PEM and reconstructs the exact JWKS (same modulus, same kid)
// Auth already holds, instead of generating a fresh key that would desync the
// registered JWKS from the stored private key.
func jwksFromPrivatePEM(privatePEM string) (jwksJSON string, err error) {
	block, _ := pem.Decode([]byte(privatePEM))
	if block == nil {
		return "", fmt.Errorf("control key PEM does not decode")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Tolerate PKCS8 too — the format is an encoding detail, not a contract.
		k8, err8 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err8 != nil {
			return "", fmt.Errorf("parse control key: %w", err)
		}
		rsaKey, ok := k8.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("control key is not RSA")
		}
		key = rsaKey
	}
	return jwksFromPublicKey(key.Public().(*rsa.PublicKey))
}

// jwksFromPublicKey builds the single-key JWK Set for an RSA public key. The
// kid is derived from the modulus, so the same key always yields the same kid.
func jwksFromPublicKey(pub *rsa.PublicKey) (string, error) {
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
		return "", fmt.Errorf("marshal jwks: %w", err)
	}
	return string(b), nil
}
