package config

// RateLimit represents rate limiting settings.
type RateLimit struct {
	Enabled bool    `json:"enabled,omitzero" yaml:"enabled"`
	RPS     float64 `json:"rps,omitzero" yaml:"rps"`
	Burst   int     `json:"burst,omitzero" yaml:"burst"`
}
