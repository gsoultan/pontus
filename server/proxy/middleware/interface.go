package middleware

import "context"

// HandlerFunc is the core logic for handling a client request.
type HandlerFunc func(ctx context.Context, s *Session) error

// Middleware defines the interface for proxy middlewares.
type Middleware interface {
	Handle(ctx context.Context, s *Session, next HandlerFunc) error
}
