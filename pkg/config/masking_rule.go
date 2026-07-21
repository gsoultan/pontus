package config

// MaskingRule defines a rule for masking sensitive data in SQL queries.
type MaskingRule struct {
	Table  string `json:"table,omitzero" yaml:"table"`
	Column string `json:"column,omitzero" yaml:"column"`
	Format string `json:"format,omitzero" yaml:"format"` // "hash", "mask", "redact"
}
