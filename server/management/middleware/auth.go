package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/gsoultan/pontus/pkg/auth"
)

// readOnlyProcedures are the RPCs a non-admin role may call.
//
// Every new RPC is admin-only by default. Adding one here is a deliberate
// security decision, not a formality.
var readOnlyProcedures = []string{
	"/GetStatus",
	// Streaming form of GetStatus; same data, same read-only classification.
	"/StreamStatus",
	"/ListProjects",
	"/GetMetricsHistory",
	"/GetTopQueriesHistory",
	"/GetLogs",
	// Read-only view of attached CDC consumers; terminating one is admin-only.
	"/ListReplicationStreams",
	"/GetServerInfo",
}

// ValidateBackend is deliberately absent from that list.
//
// It dials an address the caller supplies and reports whether the connection
// succeeded and how long it took, which is a port scanner run from wherever
// Pontus sits — usually inside the network the databases are on. It exists to
// check a backend before adding it, and adding one is admin-only, so a
// read-only dashboard user has no reason to reach it.

// Auth implements connect.Interceptor to provide token-based authentication.
type Auth struct {
	adminToken string
	issuer     *auth.Issuer
}

// NewAuth creates a new Auth interceptor.
//
// issuer must be non-nil. This interceptor used to call through
// unauthenticated whenever adminToken was empty, which left the management
// API, every mutating RPC and /metrics open by default.
func NewAuth(adminToken string, issuer *auth.Issuer) *Auth {
	return &Auth{adminToken: adminToken, issuer: issuer}
}

// WrapUnary implements connect.Interceptor.
func (a *Auth) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if isLogin(req.Spec()) {
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
		if isLogin(conn.Spec()) {
			return next(ctx, conn)
		}
		if err := a.validate(conn.RequestHeader(), conn.Spec().Procedure); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func isLogin(spec connect.Spec) bool {
	return spec != (connect.Spec{}) && strings.HasSuffix(spec.Procedure, "/Login")
}

func (a *Auth) validate(header http.Header, procedure string) error {
	authHeader := header.Get("Authorization")
	if authHeader == "" {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
	}

	provided := strings.TrimPrefix(authHeader, "Bearer ")

	// Legacy static admin token, compared in constant time so the comparison
	// does not leak the token's prefix through timing.
	if a.adminToken != "" &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(a.adminToken)) == 1 {
		return nil
	}

	if a.issuer == nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("authentication is not configured"))
	}

	claims, err := a.issuer.Verify(provided)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
	}

	if claims.Role == "admin" {
		return nil
	}

	for _, allowed := range readOnlyProcedures {
		if strings.HasSuffix(procedure, allowed) {
			return nil
		}
	}
	return connect.NewError(connect.CodePermissionDenied, errors.New("admin role required for this operation"))
}
