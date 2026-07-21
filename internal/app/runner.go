package app

import "context"

// Runner defines the interface for components that can be started and run.
type Runner interface {
	Run(ctx context.Context) error
}
