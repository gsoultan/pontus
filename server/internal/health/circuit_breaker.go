package health

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

// CircuitBreaker implements the Breaker interface using a state machine.
type CircuitBreaker struct {
	mu           sync.RWMutex
	state        State
	failures     atomic.Int64
	threshold    int64
	resetTimeout time.Duration
	lastFailure  time.Time
}

// NewCircuitBreaker creates a new CircuitBreaker.
func NewCircuitBreaker(threshold int64, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}
}

// Call executes the function and manages state transitions.
func (cb *CircuitBreaker) Call(ctx context.Context, fn func() error) error {
	if !cb.canCall() {
		return ErrCircuitOpen
	}

	err := fn()
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

func (cb *CircuitBreaker) State() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	switch cb.state {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "Half-Open"
	default:
		return "Unknown"
	}
}

func (cb *CircuitBreaker) canCall() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == StateClosed {
		return true
	}

	if cb.state == StateOpen {
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = StateHalfOpen
			return true
		}
		return false
	}

	return true // Half-Open
}

func (cb *CircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()
	if cb.state == StateHalfOpen {
		cb.state = StateOpen
		return
	}

	if cb.failures.Add(1) >= cb.threshold {
		cb.state = StateOpen
	}
}

func (cb *CircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures.Store(0)
	cb.state = StateClosed
}
