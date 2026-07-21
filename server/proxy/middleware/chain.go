package middleware

import (
	"context"
)

// Chain represents a sequence of middlewares.
type Chain []Middleware

// Handle executes the middleware chain.
func (c Chain) Handle(ctx context.Context, s *Session, final HandlerFunc) error {
	if len(c) == 0 {
		return final(ctx, s)
	}

	return c[0].Handle(ctx, s, func(ctx context.Context, s *Session) error {
		return c[1:].Handle(ctx, s, final)
	})
}
