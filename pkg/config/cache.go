package config

import "time"

// Cache represents query cache settings.
type Cache struct {
	Enabled bool          `json:"enabled,omitzero" yaml:"enabled"`
	TTL     time.Duration `json:"ttl,omitzero" yaml:"ttl"`
	MaxSize int           `json:"max_size,omitzero" yaml:"max_size"`
}
