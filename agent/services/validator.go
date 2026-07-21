package services

import "context"

// Validator defines the interface for configuration validation.
type Validator interface {
	Validate(ctx context.Context, filePath string, content string) error
}
