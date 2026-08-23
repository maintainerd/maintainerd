package setup

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// setupTokenKey is the gRPC metadata header both auth and secret read the
// bootstrap token from. One constant, because it is one contract.
const setupTokenKey = "x-setup-token"

// setupDial is the connection material for one service's gRPC SetupService.
type setupDial struct {
	// Service is the dependency's NAME — "auth", "secret". It appears in errors,
	// so it is a name and never an address or a credential.
	Service        string
	Addr           string
	CAFile         string
	ServerName     string
	ClientCertFile string
	ClientKeyFile  string
}

// dialSetupService opens a connection to a service's gRPC SetupService.
//
// PLAINTEXT ONLY WHEN NO TLS MATERIAL IS CONFIGURED AT ALL. A half-configured
// TLS setup must not silently downgrade to plaintext: the connection carries the
// bootstrap token, which is the credential that provisions the instance.
//
// ONE IMPLEMENTATION FOR BOTH SERVICES, on purpose. Core drives two SetupServices
// over the same contract, and two copies of a transport-security decision drift —
// the copy that drifts is the one that stops verifying.
func dialSetupService(d setupDial) (*grpc.ClientConn, error) {
	if d.CAFile == "" && d.ClientCertFile == "" {
		return grpc.NewClient(d.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	tlsCfg := &tls.Config{ServerName: d.ServerName}
	if d.CAFile != "" {
		caPEM, err := os.ReadFile(d.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read %s CA: %w", d.Service, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("%s CA file %q holds no valid certificate", d.Service, d.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	// Present a client cert when the peer's gRPC requires mTLS (control-plane mode).
	if d.ClientCertFile != "" && d.ClientKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(d.ClientCertFile, d.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load %s client cert: %w", d.Service, err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return grpc.NewClient(d.Addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
}
