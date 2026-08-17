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
		if err := waitCost(ctx, m.limiter, cost); err != nil {
			return err
		}
	}

	// Per-tenant limits use the configured rate; they were previously pinned
	// to a hardcoded 100rps/200burst that ignored config entirely.
	if s.State.User != "" && m.tenants != nil {
		if err := waitCost(ctx, m.tenants.Get(s.State.User), cost); err != nil {
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
	if cost > maxQueryCost {
		cost = maxQueryCost
	}
	return cost
}

// maxQueryCost bounds how many tokens one statement may be charged, so a
// pathological query cannot stall the limiter for an unbounded time.
const maxQueryCost = 50

// waitCost charges a limiter, never asking for more than it can ever grant.
//
// rate.Limiter.WaitN fails immediately when n exceeds the burst — it can never
// be satisfied, so it does not wait, it errors. estimateCost charges up to 50
// tokens for an expensive statement, so any deployment configuring a burst
// below that had every JOIN-heavy query *rejected* rather than throttled, and
// the error looked like a rate limit that no amount of waiting would clear.
//
// Clamping is the honest reading of a small burst: it says how much work may
// arrive at once, so a single statement can cost at most the whole allowance.
func waitCost(ctx context.Context, limiter *rate.Limiter, cost int) error {
	if limiter.Limit() == rate.Inf {
		return nil
	}
	if burst := limiter.Burst(); burst > 0 && cost > burst {
		cost = burst
	}
	return limiter.WaitN(ctx, cost)
}
