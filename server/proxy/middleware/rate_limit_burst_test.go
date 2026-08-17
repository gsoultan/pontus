package middleware

import (
	"context"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// rate.Limiter.WaitN fails immediately when n exceeds the burst: the request
// can never be granted, so it does not wait, it errors. estimateCost charges up
// to 50 for an expensive statement, so a deployment configuring a smaller burst
// had every complex query rejected outright — an error no amount of waiting
// would clear, from a setting that reads like "allow 10 at once".
func TestExpensiveQueryIsThrottledNotRejected(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1000), 10)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	if err := waitCost(ctx, limiter, maxQueryCost); err != nil {
		t.Fatalf("a %d-token statement against a burst of 10 was rejected: %v",
			maxQueryCost, err)
	}
}

// An unlimited limiter must not be charged at all.
func TestInfiniteLimitCostsNothing(t *testing.T) {
	limiter := rate.NewLimiter(rate.Inf, 0)
	if err := waitCost(t.Context(), limiter, maxQueryCost); err != nil {
		t.Fatalf("charging an unlimited limiter failed: %v", err)
	}
}

// The clamp must not become a free pass: the cost is still charged.
func TestClampStillCharges(t *testing.T) {
	limiter := rate.NewLimiter(rate.Limit(1), 5)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	// First call drains the bucket.
	if err := waitCost(ctx, limiter, maxQueryCost); err != nil {
		t.Fatalf("first charge failed: %v", err)
	}
	// The second cannot be granted inside the deadline at 1 token/second.
	if err := waitCost(ctx, limiter, maxQueryCost); err == nil {
		t.Error("the clamp let a second expensive query through an empty bucket")
	}
}
