package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/golang-jwt/jwt/v5"
)

// Auth implements connect.Interceptor to provide token-based authentication.
type Auth struct {
	adminToken string
	secret     []byte
}

// NewAuth creates a new Auth interceptor with the given token and secret.
func NewAuth(adminToken string, secret string) *Auth {
	return &Auth{
		adminToken: adminToken,
		secret:     []byte(secret),
	}
}

// WrapUnary implements connect.Interceptor.
func (a *Auth) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		// Allow Login without authentication
		if req.Spec() != (connect.Spec{}) && strings.HasSuffix(req.Spec().Procedure, "/Login") {
			return next(ctx, req)
		}

		if a.adminToken == "" {
			return next(ctx, req)
		}
		if err := a.validate(req.Header(), req.Spec().Procedure); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (a *Auth) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor.
func (a *Auth) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// Allow Login without authentication (though Login is unary)
		if conn.Spec() != (connect.Spec{}) && strings.HasSuffix(conn.Spec().Procedure, "/Login") {
			return next(ctx, conn)
		}

		if a.adminToken == "" {
			return next(ctx, conn)
		}
		if err := a.validate(conn.RequestHeader(), conn.Spec().Procedure); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (a *Auth) validate(header http.Header, procedure string) error {
	authHeader := header.Get("Authorization")
	if authHeader == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
	}

	providedToken := strings.TrimPrefix(authHeader, "Bearer ")

	// Support legacy admin token
	if providedToken == a.adminToken && a.adminToken != "" {
		return nil
	}

	// Validate JWT
	token, err := jwt.Parse(providedToken, func(token *jwt.Token) (any, error) {
		return a.secret, nil
	})

	if err != nil || !token.Valid {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token claims"))
	}

	role, _ := claims["role"].(string)

	// Enforce RBAC using allowlist for non-admins
	if role != "admin" {
		isPublic := false
		publicProcedures := []string{
			"/Login",
			"/GetStatus",
			"/ListProjects",
			"/GetMetricsHistory",
			"/GetTopQueriesHistory",
			"/GetLogs",
			"/GetServerInfo",
			"/ValidateBackend",
		}

		for _, proc := range publicProcedures {
			if strings.HasSuffix(procedure, proc) {
				isPublic = true
				break
			}
		}

		if !isPublic {
			return connect.NewError(connect.CodePermissionDenied, errors.New("admin role required for this operation"))
		}
	}

	return nil
}
