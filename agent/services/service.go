package services

import "context"

// Service defines the combined interface for agent operations.
type Service interface {
	Monitor
	Management
	Start(ctx context.Context) error
}
