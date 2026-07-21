package config

// Backend represents the configuration for a single backend.
type Backend struct {
	Addr       string `json:"addr,omitzero" yaml:"addr"`
	AgentAddr  string `json:"agent_addr,omitzero" yaml:"agent_addr"`
	AgentToken string `json:"agent_token,omitzero" yaml:"agent_token"`
	Zone       string `json:"zone,omitzero" yaml:"zone"`
	Role       string `json:"role,omitzero" yaml:"role"`
	Weight     int    `json:"weight,omitzero" yaml:"weight"`
}
