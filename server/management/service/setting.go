package service

import (
	"context"
)

// Setting represents a system-wide runtime setting.
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SettingProvider defines the interface for managing runtime settings.
type SettingProvider interface {
	// Get retrieves a setting by key.
	Get(ctx context.Context, key string) (string, error)

	// Set stores or updates a setting.
	Set(ctx context.Context, key string, value string) error

	// List retrieves all settings.
	List(ctx context.Context) ([]Setting, error)

	// Delete removes a setting.
	Delete(ctx context.Context, key string) error
}
