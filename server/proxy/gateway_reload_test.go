package proxy

import (
	"sync"
	"testing"

	"github.com/gsoultan/pontus/pkg/config"
	protocol2 "github.com/gsoultan/pontus/server/internal/protocol"
)

func newTestGateway(t *testing.T) *Gateway {
	t.Helper()
	handler := &mockHandler{state: protocol2.StateIdle}
	backend := &mockBackend{released: make(chan bool, 10)}
	lb := &mockBalancer{backend: backend}
	return NewGateway(handler, lb, nil, &config.Options{}, nil)
}

// handleClient reads the middleware chain on every request while UpdateConfig
// rewrites it, with no synchronization on the read side. Run with -race this
// fails against the pre-fix code.
func TestUpdateConfigIsRaceFreeUnderLoad(t *testing.T) {
	g := newTestGateway(t)

	var readers, writers sync.WaitGroup
	stop := make(chan struct{})

	// Readers stand in for the query path.
	for range 8 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					rc := g.current()
					_ = rc.chain
				}
			}
		})
	}

	// Writers hot-reload underneath them.
	for i := range 4 {
		writers.Go(func() {
			for j := range 50 {
				enabled := (i+j)%2 == 0
				g.UpdateConfig(&config.Options{
					RateLimit: &config.RateLimit{
						Enabled: enabled,
						RPS:     float64(100 + j),
						Burst:   200 + j,
					},
				})
			}
		})
	}

	// Writers finish first; only then release the readers. Waiting on both at
	// once would deadlock, since the readers exit on stop.
	writers.Wait()
	close(stop)
	readers.Wait()
}

func TestCurrentIsNeverNil(t *testing.T) {
	g := newTestGateway(t)
	if g.current() == nil {
		t.Fatal("current() returned nil")
	}
}
