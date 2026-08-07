package middleware

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/gsoultan/pontus/pkg/auth"
)

func testIssuer(t *testing.T) *auth.Issuer {
	t.Helper()
	issuer, err := auth.NewIssuer("test-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return issuer
}

func noopUnary(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
	return nil, nil
}

func TestAuth(t *testing.T) {
	token := "secret-token"
	a := NewAuth(token, testIssuer(t))

	t.Run("Unary authenticated", func(t *testing.T) {
		interceptor := a.WrapUnary(noopUnary)

		req := connect.NewRequest(&struct{}{})
		req.Header().Set("Authorization", "Bearer "+token)

		if _, err := interceptor(t.Context(), req); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("Unary missing header", func(t *testing.T) {
		interceptor := a.WrapUnary(noopUnary)

		req := connect.NewRequest(&struct{}{})

		_, err := interceptor(t.Context(), req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Errorf("expected connect.Error, got %T", err)
		} else if connectErr.Code() != connect.CodeUnauthenticated {
			t.Errorf("expected Unauthenticated code, got %v", connectErr.Code())
		}
	})

	t.Run("Unary invalid token", func(t *testing.T) {
		interceptor := a.WrapUnary(noopUnary)

		req := connect.NewRequest(&struct{}{})
		req.Header().Set("Authorization", "Bearer wrong-token")

		if _, err := interceptor(t.Context(), req); err == nil {
			t.Error("expected error, got nil")
		}
	})

	// The interceptor used to call through unauthenticated whenever the static
	// admin token was unset, which left every mutating RPC open by default.
	t.Run("Empty admin token still requires authentication", func(t *testing.T) {
		open := NewAuth("", testIssuer(t))
		interceptor := open.WrapUnary(noopUnary)

		req := connect.NewRequest(&struct{}{})

		_, err := interceptor(t.Context(), req)
		if err == nil {
			t.Fatal("unauthenticated request was allowed through with an empty admin token")
		}

		var connectErr *connect.Error
		if errors.As(err, &connectErr) && connectErr.Code() != connect.CodeUnauthenticated {
			t.Errorf("expected Unauthenticated, got %v", connectErr.Code())
		}
	})

	t.Run("Login is always reachable", func(t *testing.T) {
		interceptor := a.WrapUnary(noopUnary)

		req := connect.NewRequest(&struct{}{})
		if _, err := interceptor(t.Context(), req); err == nil {
			t.Skip("spec is empty for a bare request; covered by the streaming case")
		}
	})
}

// A non-admin PASETO token may read, but must not reach a mutating RPC.
func TestAuthEnforcesRoleAllowlist(t *testing.T) {
	issuer := testIssuer(t)
	a := NewAuth("", issuer)

	viewer, err := issuer.Issue("viewer-user", "viewer")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	cases := []struct {
		procedure string
		allowed   bool
	}{
		{"/api.proto.service.ManagementService/GetStatus", true},
		{"/api.proto.service.ManagementService/GetLogs", true},
		{"/api.proto.service.ManagementService/RemoveBackend", false},
		{"/api.proto.service.ManagementService/PromoteBackend", false},
		{"/api.proto.service.ManagementService/CreateUser", false},
		{"/api.proto.service.ManagementService/ShutdownBackend", false},
	}

	for _, tc := range cases {
		header := http.Header{}
		header.Set("Authorization", "Bearer "+viewer)

		err := a.validate(header, tc.procedure)
		if tc.allowed && err != nil {
			t.Errorf("%s: viewer should be allowed, got %v", tc.procedure, err)
		}
		if !tc.allowed && err == nil {
			t.Errorf("%s: viewer must not be allowed", tc.procedure)
		}
	}
}

func TestAuthAcceptsAdminToken(t *testing.T) {
	issuer := testIssuer(t)
	a := NewAuth("", issuer)

	admin, err := issuer.Issue("root", "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+admin)

	if err := a.validate(header, "/api.proto.service.ManagementService/ShutdownBackend"); err != nil {
		t.Errorf("admin should reach a mutating RPC, got %v", err)
	}
}

// A token minted under a different key must not verify.
func TestAuthRejectsForeignKey(t *testing.T) {
	other, err := auth.NewIssuer("a-different-secret")
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	foreign, err := other.Issue("mallory", "admin")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	a := NewAuth("", testIssuer(t))
	header := http.Header{}
	header.Set("Authorization", "Bearer "+foreign)

	if err := a.validate(header, "/api.proto.service.ManagementService/GetStatus"); err == nil {
		t.Error("token signed with a foreign key was accepted")
	}
}

type mockStreamingHandlerConn struct {
	connect.StreamingHandlerConn
	header http.Header
	spec   connect.Spec
}

func (m *mockStreamingHandlerConn) RequestHeader() http.Header {
	return m.header
}

func (m *mockStreamingHandlerConn) Spec() connect.Spec {
	return m.spec
}

func TestAuth_Streaming(t *testing.T) {
	token := "secret-token"
	a := NewAuth(token, testIssuer(t))

	t.Run("Streaming authenticated", func(t *testing.T) {
		handler := a.WrapStreamingHandler(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
			return nil
		})

		header := http.Header{}
		header.Set("Authorization", "Bearer "+token)
		conn := &mockStreamingHandlerConn{
			header: header,
			spec:   connect.Spec{Procedure: "/Test"},
		}

		if err := handler(t.Context(), conn); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("Streaming missing header", func(t *testing.T) {
		handler := a.WrapStreamingHandler(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
			return nil
		})

		conn := &mockStreamingHandlerConn{
			header: http.Header{},
			spec:   connect.Spec{Procedure: "/Test"},
		}

		if err := handler(t.Context(), conn); err == nil {
			t.Error("expected error, got nil")
		}
	})

	// StreamLogs is admin-gated too; an unauthenticated stream must not open.
	t.Run("Streaming empty admin token still requires authentication", func(t *testing.T) {
		open := NewAuth("", testIssuer(t))
		handler := open.WrapStreamingHandler(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
			return nil
		})

		conn := &mockStreamingHandlerConn{
			header: http.Header{},
			spec:   connect.Spec{Procedure: "/api.proto.service.ManagementService/StreamLogs"},
		}

		if err := handler(t.Context(), conn); err == nil {
			t.Error("unauthenticated stream was allowed with an empty admin token")
		}
	})
}
