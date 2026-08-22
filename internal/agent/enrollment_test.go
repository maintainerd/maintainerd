package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnrollConsumesJoinTokenAndSignsCSR(t *testing.T) {
	repo := newFake("")
	joinToken := "join-token"
	repo.row.JoinTokenHash = joinTokenHash(joinToken)
	caCertPEM, caKeyPEM := testAgentCA(t)
	csrPEM := testAgentCSR(t, repo.row.AgentUuid.String())

	enrolled, err := NewService(repo).Enroll(context.Background(), EnrollInput{
		AgentUUID: repo.row.AgentUuid,
		JoinToken: joinToken,
		CSRPem:    csrPEM,
		CACertPEM: caCertPEM,
		CAKeyPEM:  caKeyPEM,
		CertTTL:   time.Hour,
	})
	require.NoError(t, err)
	require.NotNil(t, enrolled)

	assert.Equal(t, caCertPEM, enrolled.CACertPEM)
	assert.True(t, repo.row.JoinTokenUsedAt.Valid)
	assert.Equal(t, string(enrolled.CertificatePEM), repo.row.ClientCertPem)
	assert.True(t, enrolled.ExpiresAt.After(time.Now()))
	assert.Equal(t, repo.row.AgentUuid, enrolled.Agent.UUID)

	cert := parseTestCert(t, enrolled.CertificatePEM)
	assert.Equal(t, "maintainerd-agent:"+repo.row.AgentUuid.String(), cert.Subject.CommonName)
	assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth)
	require.Len(t, cert.URIs, 1)
	assert.Equal(t, "spiffe://maintainerd/agent/"+repo.row.AgentUuid.String(), cert.URIs[0].String())
}

func TestEnrollRejectsInvalidOrUsedJoinToken(t *testing.T) {
	t.Run("invalid token", func(t *testing.T) {
		repo := newFake("")
		repo.row.JoinTokenHash = joinTokenHash("real-token")

		_, err := NewService(repo).Enroll(context.Background(), EnrollInput{
			AgentUUID: repo.row.AgentUuid,
			JoinToken: "wrong-token",
			CSRPem:    []byte("not reached"),
		})
		require.Error(t, err)
		assert.False(t, repo.row.JoinTokenUsedAt.Valid)
		assert.Empty(t, repo.row.ClientCertPem)
	})

	t.Run("used token", func(t *testing.T) {
		repo := newFake("")
		repo.row.JoinTokenHash = joinTokenHash("real-token")
		repo.row.JoinTokenUsedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}

		_, err := NewService(repo).Enroll(context.Background(), EnrollInput{
			AgentUUID: repo.row.AgentUuid,
			JoinToken: "real-token",
			CSRPem:    []byte("not reached"),
		})
		require.Error(t, err)
		assert.Empty(t, repo.row.ClientCertPem)
	})
}

func testAgentCA(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "maintainerd-agent-test-ca"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	require.NoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func testAgentCSR(t *testing.T, agentUUID string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	agentURI, err := url.Parse("spiffe://maintainerd/agent/" + agentUUID)
	require.NoError(t, err)
	tpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "agent"},
		URIs:    []*url.URL{agentURI},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tpl, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

func parseTestCert(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}
