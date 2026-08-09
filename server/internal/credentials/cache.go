package credentials

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Defaults for the cache. Every one is a bound rather than a preference.
const (
	// DefaultTTL is how long a hit is trusted. A password changed in the
	// database takes effect after at most this long.
	DefaultTTL = 5 * time.Minute

	// DefaultNegativeTTL is shorter: a user that does not exist yet is a more
	// likely thing to change than one that does, and a long negative entry
	// makes creating a role feel broken.
	DefaultNegativeTTL = 30 * time.Second

	// DefaultMaxEntries bounds the map. The key is a client-supplied user name.
	DefaultMaxEntries = 1024
)

// entry is one cached answer. A miss is cached too — see Cache.
type entry struct {
	verifier Verifier
	err      error
	expires  time.Time
}

// Cache wraps a Store with a bounded, expiring memory.
//
// Two properties here are security requirements rather than optimisations:
//
//   - It is **bounded**. The key is the user name from a startup packet, which
//     anyone who can reach the port chooses. An unbounded map keyed on that is
//     a remote memory exhaustion, the same shape as the tenant rate-limiter
//     that had to be capped.
//   - It caches **misses**. Without that, every connection attempt for a name
//     that does not exist becomes a query against the primary, so an attacker
//     walking a username list turns one cheap TCP connection each into real
//     database work — an amplification with Pontus as the amplifier.
type Cache struct {
	inner Store

	ttl         time.Duration
	negativeTTL time.Duration
	maxEntries  int

	mu      sync.Mutex
	entries map[string]entry

	// now is swappable so expiry can be tested without sleeping.
	now func() time.Time
}

// NewCache wraps a store. Non-positive settings take the defaults rather than
// disabling the bound, because "no limit" is not a sensible reading of zero for
// any of them.
func NewCache(inner Store, ttl, negativeTTL time.Duration, maxEntries int) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if negativeTTL <= 0 {
		negativeTTL = DefaultNegativeTTL
	}
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &Cache{
		inner:       inner,
		ttl:         ttl,
		negativeTTL: negativeTTL,
		maxEntries:  maxEntries,
		entries:     make(map[string]entry),
		now:         time.Now,
	}
}

// Lookup returns a cached answer when there is a live one, and otherwise asks
// the underlying store and remembers what it said.
func (c *Cache) Lookup(ctx context.Context, user string) (Verifier, error) {
	if cached, ok := c.get(user); ok {
		return cached.verifier, cached.err
	}

	verifier, err := c.inner.Lookup(ctx, user)

	// A transport failure is not an answer about the user, so it must not be
	// remembered: caching it would keep a whole deployment locked out for the
	// TTL after one blip. Only a definite "no such user" is negative-cacheable.
	if err != nil && !errors.Is(err, ErrUnknownUser) && !errors.Is(err, ErrUnsupportedVerifier) {
		return verifier, err
	}

	c.put(user, entry{verifier: verifier, err: err, expires: c.expiryFor(err)})
	return verifier, err
}

func (c *Cache) expiryFor(err error) time.Time {
	if err != nil {
		return c.now().Add(c.negativeTTL)
	}
	return c.now().Add(c.ttl)
}

func (c *Cache) get(user string) (entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	found, ok := c.entries[user]
	if !ok {
		return entry{}, false
	}
	if c.now().After(found.expires) {
		delete(c.entries, user)
		return entry{}, false
	}
	return found, true
}

func (c *Cache) put(user string, e entry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxEntries {
		c.evictLocked()
	}
	c.entries[user] = e
}

// evictLocked makes room. Expired entries go first; if none are expired, the
// entry closest to expiry goes, which under a uniform TTL is the oldest.
//
// Not an LRU: this map is small, refilled from a query that takes microseconds,
// and its purpose is to bound memory rather than to maximise hits. A precise
// LRU here would be more machinery guarding the same ceiling.
func (c *Cache) evictLocked() {
	now := c.now()
	for user, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, user)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}

	var victim string
	var soonest time.Time
	first := true
	for user, e := range c.entries {
		if first || e.expires.Before(soonest) {
			victim, soonest, first = user, e.expires, false
		}
	}
	delete(c.entries, victim)
}

// Forget drops a user's cached answer, so a password change can be made to
// take effect without waiting out the TTL.
func (c *Cache) Forget(user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, user)
}

// Flush empties the cache.
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]entry)
}

// Len reports how many answers are held.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
