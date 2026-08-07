package middleware

import (
	"context"
	"strings"

	"github.com/gsoultan/pontus/server/internal/protocol"
	"golang.org/x/time/rate"
)

type RateLimit struct {
	limiter *rate.Limiter
	tenants *TenantLimiters
}

func NewRateLimit(limiter *rate.Limiter, tenants *TenantLimiters) *RateLimit {
	return &RateLimit{limiter: limiter, tenants: tenants}
}

func (m *RateLimit) Handle(ctx context.Context, s *Session, next HandlerFunc) error {
	cost := m.estimateCost(s.Normalized)

	if m.limiter != nil {
		if err := m.limiter.WaitN(ctx, cost); err != nil {
			return err
		}
	}

	// Per-tenant limits use the configured rate; they were previously pinned
	// to a hardcoded 100rps/200burst that ignored config entirely.
	if s.State.User != "" && m.tenants != nil {
		if err := m.tenants.Get(s.State.User).WaitN(ctx, cost); err != nil {
			return err
		}
	}

	return next(ctx, s)
}

func (m *RateLimit) estimateCost(query string) int {
	if query == "" {
		return 1
	}

	cost := 1
	tokens := protocol.Tokenize(query)
	for t := range tokens {
		if t.Type != protocol.TokenKeyword {
			continue
		}

		switch strings.ToUpper(t.Value) {
		case "JOIN":
			cost += 5
		case "GROUP":
			cost += 3
		case "ORDER":
			cost += 2
		case "UNION":
			cost += 4
		case "SELECT":
			// Check for SELECT *
			cost += 1
		case "UPDATE", "DELETE", "INSERT":
			cost += 2
		}
	}

	// Cap cost to prevent stalling the limiter too long
	if cost > 50 {
		cost = 50
	}
	return cost
}
