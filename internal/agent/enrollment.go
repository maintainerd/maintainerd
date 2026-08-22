package agent

import (
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

type SignAgentCSRInput struct {
	AgentUUID uuid.UUID
	CSRPEM    []byte
	CACertPEM []byte
	CAKeyPEM  []byte
	TTL       time.Duration
}

// SignAgentCSR signs an agent CSR with Core's configured agent-client CA. The
// issued certificate is a client-auth-only credential whose URI SAN pins it to
// the agent UUID; the gateway still binds the bearer-token subject separately.
func SignAgentCSR(in SignAgentCSRInput) ([]byte, time.Time, error) {
	if in.AgentUUID == uuid.Nil {
		return nil, time.Time{}, fmt.Errorf("agent_uuid is required")
	}
	csrBlock, _ := pem.Decode(in.CSRPEM)
	if csrBlock == nil || csrBlock.Type != "CERTIFICATE REQUEST" {
		return nil, time.Time{}, fmt.Errorf("csr_pem must contain a PEM certificate request")
	}
	csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("parse csr: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, time.Time{}, fmt.Errorf("verify csr signature: %w", err)
	}
	ca, err := parseCACert(in.CACertPEM)
	if err != nil {
		return nil, time.Time{}, err
	}
	key, err := parseCAKey(in.CAKeyPEM)
	if err != nil {
		return nil, time.Time{}, err
	}
	if in.TTL <= 0 {
		in.TTL = 24 * time.Hour
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("generate serial: %w", err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(in.TTL)
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "maintainerd-agent:" + in.AgentUUID.String(),
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              expiresAt,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	tpl.DNSNames = append(tpl.DNSNames, csr.DNSNames...)
	tpl.IPAddresses = append(tpl.IPAddresses, csr.IPAddresses...)
	tpl.URIs = append(tpl.URIs, csr.URIs...)
	certDER, err := x509.CreateCertificate(rand.Reader, tpl, ca, csr.PublicKey, key)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("sign certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), expiresAt, nil
}

func parseCACert(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("agent_ca_cert_pem must contain a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse agent CA cert: %w", err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("agent CA certificate is not a CA")
	}
	return cert, nil
}

func parseCAKey(pemBytes []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("agent_ca_key_pem must contain a PEM private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("agent CA PKCS8 key is not a signer")
		}
		return signer, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unsupported agent CA private key format")
}
