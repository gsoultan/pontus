package config

// Firewall represents SQL firewall rules.
type Firewall struct {
	Enabled           bool          `json:"enabled,omitzero" yaml:"enabled"`
	BlockedWords      []string      `json:"blocked_words,omitzero" yaml:"blocked_words"`
	Patterns          []string      `json:"patterns,omitzero" yaml:"patterns"`
	MaxResponseSizeMB int64         `json:"max_response_size_mb,omitzero" yaml:"max_response_size_mb"`
	MaskingRules      []MaskingRule `json:"masking_rules,omitzero" yaml:"masking_rules"`
}
