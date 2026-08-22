package grpcserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	corev1 "github.com/maintainerd/core/gen/maintainerd/core/v1"
)

type TLSOptions struct {
	CertFile          string
	KeyFile           string
	ClientCAFile      string
	RequireClientCert bool
}

// RequireClientCertUnaryInterceptor enforces mTLS for every AgentGateway RPC
// except Enroll. Enrollment is the bootstrap exchange that creates the client
// certificate, and is protected by the one-time join token instead.
func RequireClientCertUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if info.FullMethod == corev1.AgentGatewayService_Enroll_FullMethodName || hasHealthPrefix(info.FullMethod) {
			return handler(ctx, req)
		}
		p, ok := peer.FromContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing peer information")
		}
		tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok || len(tlsInfo.State.PeerCertificates) == 0 {
			return nil, status.Error(codes.Unauthenticated, "client certificate required")
		}
		if len(tlsInfo.State.VerifiedChains) == 0 {
			return nil, status.Error(codes.Unauthenticated, "client certificate is not trusted")
		}
		return handler(ctx, req)
	}
}

func hasHealthPrefix(method string) bool {
	return len(method) >= len(healthServicePrefix) && method[:len(healthServicePrefix)] == healthServicePrefix
}

func serverTLSConfig(opts TLSOptions) (*tls.Config, error) {
	if opts.CertFile == "" && opts.KeyFile == "" && opts.ClientCAFile == "" {
		return nil, nil
	}
	if opts.CertFile == "" || opts.KeyFile == "" {
		return nil, fmt.Errorf("GRPC_TLS_CERT_FILE and GRPC_TLS_KEY_FILE are required when gRPC TLS is configured")
	}
	cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load gRPC TLS keypair: %w", err)
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if opts.ClientCAFile != "" {
		pool, err := loadCertPool(opts.ClientCAFile)
		if err != nil {
			return nil, err
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
	}
	if opts.RequireClientCert {
		if opts.ClientCAFile == "" {
			return nil, fmt.Errorf("GRPC_CLIENT_CA_FILE is required when client certificates are required")
		}
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
	}
	return cfg, nil
}

func loadCertPool(path string) (*x509.CertPool, error) {
	pemBytes, err := osReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read client CA file %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("client CA file %q contains no PEM certificate", path)
	}
	return pool, nil
}

var osReadFile = os.ReadFile
