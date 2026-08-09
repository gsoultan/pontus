package credentials

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// countingStore records how often it was actually asked.
type countingStore struct {
	mu      sync.Mutex
	calls   int
	answers map[string]Verifier
	fail    error
}

func (s *countingStore) Lookup(_ context.Context, user string) (Verifier, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.fail != nil {
		return Verifier{}, s.fail
	}
	v, ok := s.answers[user]
	if !ok {
		return Verifier{}, fmt.Errorf("%w: %q", ErrUnknownUser, user)
	}
	return v, nil
}

func (s *countingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestCacheServesRepeatLookups(t *testing.T) {
	inner := &countingStore{answers: map[string]Verifier{"alice": {Method: MethodMD5, MD5: "x"}}}
	c := NewCache(inner, time.Minute, time.Minute, 16)

	for range 5 {
		if _, err := c.Lookup(t.Context(), "alice"); err != nil {
			t.Fatalf("Lookup: %v", err)
		}
	}
	if inner.count() != 1 {
		t.Errorf("asked the store %d times for one user, want 1", inner.count())
	}
}

// Without negative caching, an attacker walking a username list turns each
// cheap TCP connection into real work on the primary — an amplification with
// Pontus as the amplifier.
func TestCacheRemembersUnknownUsers(t *testing.T) {
	inner := &countingStore{answers: map[string]Verifier{}}
	c := NewCache(inner, time.Minute, time.Minute, 16)

	for range 10 {
		if _, err := c.Lookup(t.Context(), "nobody"); !errors.Is(err, ErrUnknownUser) {
			t.Fatalf("err = %v, want ErrUnknownUser", err)
		}
	}
	if inner.count() != 1 {
		t.Errorf("an unknown user reached the database %d times, want 1; "+
			"repeated attempts must not amplify into database load", inner.count())
	}
}

// A transport failure says nothing about the user. Remembering it would keep a
// deployment locked out for the whole TTL after a single blip.
func TestCacheDoesNotRememberTransportFailures(t *testing.T) {
	inner := &countingStore{fail: errors.New("connection refused")}
	c := NewCache(inner, time.Minute, time.Minute, 16)

	for range 3 {
		if _, err := c.Lookup(t.Context(), "alice"); err == nil {
			t.Fatal("expected the transport error to surface")
		}
	}
	if inner.count() != 3 {
		t.Errorf("a transport failure was cached (%d calls for 3 lookups); "+
			"one blip would lock everyone out for the TTL", inner.count())
	}
}

func TestCacheExpiresEntries(t *testing.T) {
	inner := &countingStore{answers: map[string]Verifier{"alice": {Method: MethodMD5, MD5: "x"}}}
	c := NewCache(inner, time.Minute, time.Minute, 16)

	now := time.Now()
	c.now = func() time.Time { return now }

	if _, err := c.Lookup(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute) // past the TTL
	if _, err := c.Lookup(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	if inner.count() != 2 {
		t.Errorf("store asked %d times, want 2 — the entry did not expire", inner.count())
	}
}

// The key is a user name from a startup packet, chosen by anyone who can reach
// the port. An unbounded map keyed on that is a remote memory exhaustion.
func TestCacheIsBounded(t *testing.T) {
	inner := &countingStore{answers: map[string]Verifier{}}
	const limit = 32
	c := NewCache(inner, time.Minute, time.Minute, limit)

	for i := range 5000 {
		_, _ = c.Lookup(t.Context(), "user-"+strconv.Itoa(i))
	}

	if got := c.Len(); got > limit {
		t.Errorf("cache holds %d entries with a limit of %d; a client chooses these keys", got, limit)
	}
}

// Non-positive settings must take the defaults, not disable the bound.
func TestCacheZeroSettingsTakeDefaults(t *testing.T) {
	c := NewCache(&countingStore{answers: map[string]Verifier{}}, 0, 0, 0)
	if c.ttl != DefaultTTL || c.negativeTTL != DefaultNegativeTTL || c.maxEntries != DefaultMaxEntries {
		t.Errorf("zero settings gave ttl=%v negative=%v max=%d",
			c.ttl, c.negativeTTL, c.maxEntries)
	}
}

// A password change should be applicable without waiting out the TTL.
func TestForgetDropsAnEntry(t *testing.T) {
	inner := &countingStore{answers: map[string]Verifier{"alice": {Method: MethodMD5, MD5: "x"}}}
	c := NewCache(inner, time.Hour, time.Hour, 16)

	_, _ = c.Lookup(t.Context(), "alice")
	c.Forget("alice")
	_, _ = c.Lookup(t.Context(), "alice")

	if inner.count() != 2 {
		t.Errorf("store asked %d times, want 2 — Forget did not drop the entry", inner.count())
	}
}

func TestCacheIsConcurrencySafe(t *testing.T) {
	inner := &countingStore{answers: map[string]Verifier{"alice": {Method: MethodMD5, MD5: "x"}}}
	c := NewCache(inner, time.Minute, time.Minute, 64)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			_, _ = c.Lookup(context.Background(), "alice")
			_, _ = c.Lookup(context.Background(), "user-"+strconv.Itoa(i))
			c.Len()
		})
	}
	wg.Wait()
}
