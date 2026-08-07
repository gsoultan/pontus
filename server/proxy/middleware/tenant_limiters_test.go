package middleware

import (
	"fmt"
	"sync"
	"testing"

	"golang.org/x/time/rate"
)

// The tenant map is keyed by a client-supplied username. Before this bound, a
// client reconnecting under fresh usernames grew it without limit.
func TestTenantLimitersAreBounded(t *testing.T) {
	const max = 64
	limiters := NewTenantLimiters(rate.Limit(100), 200, max)

	for i := range 10_000 {
		limiters.Get(fmt.Sprintf("attacker-%d", i))
	}

	if got := limiters.Len(); got > max {
		t.Fatalf("retained %d tenants, want at most %d", got, max)
	}
}

func TestTenantLimitersReuseEntries(t *testing.T) {
	limiters := NewTenantLimiters(rate.Limit(100), 200, 16)

	first := limiters.Get("alice")
	second := limiters.Get("alice")

	if first != second {
		t.Error("the same tenant must reuse its limiter, not allocate a new one")
	}
	if got := limiters.Len(); got != 1 {
		t.Errorf("Len = %d, want 1", got)
	}
}

func TestTenantLimitersUseConfiguredRate(t *testing.T) {
	limiters := NewTenantLimiters(rate.Limit(7), 13, 16)

	limiter := limiters.Get("alice")
	if limiter.Limit() != rate.Limit(7) {
		t.Errorf("Limit = %v, want 7 (config was ignored)", limiter.Limit())
	}
	if limiter.Burst() != 13 {
		t.Errorf("Burst = %d, want 13 (config was ignored)", limiter.Burst())
	}
}

func TestTenantLimitersConcurrentAccess(t *testing.T) {
	limiters := NewTenantLimiters(rate.Limit(100), 200, 128)

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			for j := range 200 {
				limiters.Get(fmt.Sprintf("tenant-%d", (i*200+j)%512))
			}
		})
	}
	wg.Wait()

	if got := limiters.Len(); got > 128 {
		t.Fatalf("retained %d tenants under concurrency, want at most 128", got)
	}
}
