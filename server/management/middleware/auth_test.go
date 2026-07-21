package middleware

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

func TestAuth(t *testing.T) {
	token := "secret-token"
	auth := NewAuth(token, "test-secret")

	t.Run("Unary authenticated", func(t *testing.T) {
		interceptor := auth.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return nil, nil
		})

		req := connect.NewRequest(&struct{}{})
		req.Header().Set("Authorization", "Bearer "+token)

		_, err := interceptor(t.Context(), req)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("Unary missing header", func(t *testing.T) {
		interceptor := auth.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return nil, nil
		})

		req := connect.NewRequest(&struct{}{})

		_, err := interceptor(t.Context(), req)
		if err == nil {
			t.Error("expected error, got nil")
		}

		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Errorf("expected connect.Error, got %T", err)
		} else if connectErr.Code() != connect.CodeUnauthenticated {
			t.Errorf("expected Unauthenticated code, got %v", connectErr.Code())
		}
	})

	t.Run("Unary invalid token", func(t *testing.T) {
		interceptor := auth.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return nil, nil
		})

		req := connect.NewRequest(&struct{}{})
		req.Header().Set("Authorization", "Bearer wrong-token")

		_, err := interceptor(t.Context(), req)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})

	t.Run("Empty token allows all", func(t *testing.T) {
		authEmpty := NewAuth("", "")
		interceptor := authEmpty.WrapUnary(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return nil, nil
		})

		req := connect.NewRequest(&struct{}{})
		_, err := interceptor(t.Context(), req)
		if err != nil {
			t.Errorf("expected no error when token is empty, got %v", err)
		}
	})
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
	auth := NewAuth(token, "test-secret")

	t.Run("Streaming authenticated", func(t *testing.T) {
		handler := auth.WrapStreamingHandler(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
			return nil
		})

		header := http.Header{}
		header.Set("Authorization", "Bearer "+token)
		conn := &mockStreamingHandlerConn{
			header: header,
			spec:   connect.Spec{Procedure: "/Test"},
		}

		err := handler(t.Context(), conn)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("Streaming missing header", func(t *testing.T) {
		handler := auth.WrapStreamingHandler(func(ctx context.Context, conn connect.StreamingHandlerConn) error {
			return nil
		})

		conn := &mockStreamingHandlerConn{
			header: http.Header{},
			spec:   connect.Spec{Procedure: "/Test"},
		}

		err := handler(t.Context(), conn)
		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}
