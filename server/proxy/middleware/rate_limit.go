package middleware

import (
	"context"
	"strings"
	"sync"

	"github.com/gsoultan/pontus/server/internal/protocol"
	"golang.org/x/time/rate"
)

type RateLimit struct {
	limiter        *rate.Limiter
	tenantLimiters *sync.Map
}

func NewRateLimit(limiter *rate.Limiter, tenantLimiters *sync.Map) *RateLimit {
	return &RateLimit{limiter: limiter, tenantLimiters: tenantLimiters}
}

func (m *RateLimit) Handle(ctx context.Context, s *Session, next HandlerFunc) error {
	cost := m.estimateCost(s.Normalized)

	if m.limiter != nil {
		if err := m.limiter.WaitN(ctx, cost); err != nil {
			return err
		}
	}

	if s.State.User != "" {
		limiter, _ := m.tenantLimiters.LoadOrStore(s.State.User, rate.NewLimiter(rate.Limit(100), 200))
		if err := limiter.(*rate.Limiter).WaitN(ctx, cost); err != nil {
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
