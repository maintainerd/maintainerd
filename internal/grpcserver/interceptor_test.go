package grpcserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	corev1 "github.com/maintainerd/core/gen/maintainerd/core/v1"
)

func ctxWithBearer(token string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+token))
}

func staticVerify(claims *Claims, err error) VerifyFunc {
	return func(context.Context, string) (*Claims, error) { return claims, err }
}

func TestAuthUnaryInterceptor(t *testing.T) {
	registerMethod := corev1.AgentGatewayService_Register_FullMethodName

	tests := []struct {
		name       string
		ctx        context.Context
		method     string
		verify     VerifyFunc
		wantCode   codes.Code // codes.OK means the handler must run
		wantClaims bool
	}{
		{
			name:     "health is exempt even without a token",
			ctx:      context.Background(),
			method:   "/grpc.health.v1.Health/Check",
			verify:   staticVerify(nil, errors.New("must not be called")),
			wantCode: codes.OK,
		},
		{
			name:     "missing token is unauthenticated",
			ctx:      context.Background(),
			method:   registerMethod,
			verify:   staticVerify(&Claims{Subject: "s"}, nil),
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "invalid token is unauthenticated",
			ctx:      ctxWithBearer("forged"),
			method:   registerMethod,
			verify:   staticVerify(nil, errors.New("bad signature")),
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "unmapped method denied even when authenticated",
			ctx:      ctxWithBearer("ok"),
			method:   "/maintainerd.core.v1.AgentGatewayService/NotARealMethod",
			verify:   staticVerify(&Claims{Subject: "s", Permissions: []string{gatewayPermission}}, nil),
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "missing permission denied",
			ctx:      ctxWithBearer("ok"),
			method:   registerMethod,
			verify:   staticVerify(&Claims{Subject: "s", Permissions: []string{"core:other"}}, nil),
			wantCode: codes.PermissionDenied,
		},
		{
			name:       "permission via permissions array claim allows",
			ctx:        ctxWithBearer("ok"),
			method:     registerMethod,
			verify:     staticVerify(&Claims{Subject: "s", Permissions: []string{gatewayPermission}}, nil),
			wantCode:   codes.OK,
			wantClaims: true,
		},
		{
			name:       "permission via scope claim allows",
			ctx:        ctxWithBearer("ok"),
			method:     corev1.AgentGatewayService_ReportStatus_FullMethodName,
			verify:     staticVerify(&Claims{Subject: "s", Scopes: []string{gatewayPermission}}, nil),
			wantCode:   codes.OK,
			wantClaims: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var handlerCtx context.Context
			handler := func(ctx context.Context, _ any) (any, error) {
				handlerCtx = ctx
				return "ok", nil
			}
			ic := AuthUnaryInterceptor(tt.verify)
			_, err := ic(tt.ctx, nil, &grpc.UnaryServerInfo{FullMethod: tt.method}, handler)

			if tt.wantCode == codes.OK {
				require.NoError(t, err)
				if tt.wantClaims {
					claims, ok := ClaimsFromContext(handlerCtx)
					require.True(t, ok, "verified claims must reach the handler")
					assert.Equal(t, "s", claims.Subject)
				}
				return
			}
			require.Error(t, err)
			assert.Equal(t, tt.wantCode, status.Code(err))
			assert.Nil(t, handlerCtx, "handler must not run when the guard denies")
		})
	}
}

// TestEveryGatewayMethodIsMapped pins the allowlist to the generated service
// descriptor: a new RPC added to the proto without a permission decision must
// fail THIS test instead of shipping (it would be denied at runtime, but the
// denial should be a deliberate choice, not a surprise in production).
func TestEveryGatewayMethodIsMapped(t *testing.T) {
	sd := corev1.AgentGatewayService_ServiceDesc
	for _, m := range sd.Methods {
		full := "/" + sd.ServiceName + "/" + m.MethodName
		_, ok := methodPermissions[full]
		assert.True(t, ok, "method %s has no permission mapping", full)
	}
}
