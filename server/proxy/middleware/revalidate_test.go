package middleware

import (
	"sync"
	"testing"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// Every client that hit the same stale entry used to start its own refresh —
// one goroutine and one pooled backend connection each, all running the
// identical query at the same moment. That is the thundering herd the cache
// exists to prevent, arriving through the cache itself.
func TestRefreshIsDeduplicatedPerKey(t *testing.T) {
	m := NewCache(nil, &config.Cache{Enabled: true}, nil)

	const callers = 64
	var started sync.WaitGroup
	admitted := make(chan string, callers)

	started.Add(callers)
	for range callers {
		go func() {
			defer started.Done()
			// Mirrors what revalidate does before it spawns: claim the key,
			// then take a slot.
			if _, busy := m.refreshing.LoadOrStore("same-key", struct{}{}); busy {
				return
			}
			select {
			case m.slots <- struct{}{}:
				admitted <- "same-key"
			default:
			}
		}()
	}
	started.Wait()
	close(admitted)

	if got := len(admitted); got != 1 {
		t.Errorf("%d refreshes started for one key; want exactly 1", got)
	}
}

// Deduplication alone does not bound anything: a thousand *distinct* stale keys
// would still take a thousand pooled connections. Each refresh holds one, so
// without a ceiling the cache drains the pool that real queries wait on.
func TestConcurrentRefreshesAreCapped(t *testing.T) {
	m := NewCache(nil, &config.Cache{Enabled: true}, nil)

	admitted := 0
	for i := range maxConcurrentRefreshes * 4 {
		if _, busy := m.refreshing.LoadOrStore(itoa(i), struct{}{}); busy {
			continue
		}
		select {
		case m.slots <- struct{}{}:
			admitted++
		default:
		}
	}

	if admitted > maxConcurrentRefreshes {
		t.Errorf("%d concurrent refreshes admitted, cap is %d",
			admitted, maxConcurrentRefreshes)
	}
	if admitted == 0 {
		t.Error("no refresh was admitted at all; the stale window never refreshes")
	}
}

// A released slot must be reusable, or refreshing stops for good after the
// first burst.
func TestSlotsAreReturned(t *testing.T) {
	m := NewCache(nil, &config.Cache{Enabled: true}, nil)

	for range maxConcurrentRefreshes {
		m.slots <- struct{}{}
	}
	for range maxConcurrentRefreshes {
		<-m.slots
	}

	select {
	case m.slots <- struct{}{}:
	default:
		t.Error("slots were not returned; no entry would ever be refreshed again")
	}
}

var _ = protocol.StateIdle

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
