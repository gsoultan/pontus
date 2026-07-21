package health

import (
	"context"
	"errors"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

// Breaker defines the interface for a circuit breaker.
type Breaker interface {
	// Call executes the given function if the circuit is not open.
	Call(ctx context.Context, fn func() error) error

	// State returns the current state of the breaker.
	State() string
}
