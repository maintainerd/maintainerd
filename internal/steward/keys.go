package steward

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

// GenerateClientKey mints a service client's private_key_jwt signing key. Auth
// is handed only the PUBLIC JWKS (RFC 7523), so the steward keeps the private
// key and auth never holds a credential that could impersonate the service.
// Returns the private key as PEM and the public JWK Set as JSON.
//
// Both transports use this: the setup-window applier while auth's SetupService
// is still open, and the regular-surface applier afterwards. Keeping one
// implementation is what makes a client provisioned by either path verifiable by
// the same stored PEM.
func GenerateClientKey() (privatePEM string, jwksJSON string, err error) {
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

// JWKSFromPrivatePEM re-derives the public JWK Set from a persisted private
// key. This is what makes a keypair mint-once: a re-run reuses the stored PEM
// and reconstructs the exact JWKS (same modulus, same kid) auth already holds,
// instead of generating a fresh key that would desync the registered JWKS from
// the stored private key.
func JWKSFromPrivatePEM(privatePEM string) (jwksJSON string, err error) {
	key, err := ParseRSAPrivateKey(privatePEM)
	if err != nil {
		return "", err
	}
	return jwksFromPublicKey(key.Public().(*rsa.PublicKey))
}

// ParseRSAPrivateKey decodes a PEM-encoded RSA private key in either PKCS#1 or
// PKCS#8 form — the encoding is an implementation detail of whoever wrote the
// key, not a contract.
func ParseRSAPrivateKey(privatePEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privatePEM))
	if block == nil {
		return nil, fmt.Errorf("control key PEM does not decode")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err == nil {
		return key, nil
	}
	k8, err8 := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err8 != nil {
		return nil, fmt.Errorf("parse control key: %w", err)
	}
	rsaKey, ok := k8.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("control key is not RSA")
	}
	return rsaKey, nil
}

// KeyID returns the JWK "kid" for an RSA public key. It is derived from the
// modulus, so the same key always yields the same kid — auth looks the
// verification key up by kid, and a kid that drifted between runs would break
// every assertion signed afterwards.
func KeyID(pub *rsa.PublicKey) string {
	sum := sha256.Sum256(pub.N.Bytes())
	return fmt.Sprintf("%x", sum)[:16]
}

// jwksFromPublicKey builds the single-key JWK Set for an RSA public key.
func jwksFromPublicKey(pub *rsa.PublicKey) (string, error) {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())

	jwks := map[string]any{
		"keys": []map[string]any{
			{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": KeyID(pub), "n": n, "e": e},
		},
	}
	b, err := json.Marshal(jwks)
	if err != nil {
		return "", fmt.Errorf("marshal jwks: %w", err)
	}
	return string(b), nil
}
