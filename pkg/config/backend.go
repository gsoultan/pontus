package config

// Backend represents the configuration for a single backend.
type Backend struct {
	Addr       string `json:"addr,omitzero" yaml:"addr"`
	AgentAddr  string `json:"agent_addr,omitzero" yaml:"agent_addr"`
	AgentToken string `json:"agent_token,omitzero" yaml:"agent_token"`
	Zone       string `json:"zone,omitzero" yaml:"zone"`
	Role       string `json:"role,omitzero" yaml:"role"`
	Weight     int    `json:"weight,omitzero" yaml:"weight"`
	// AdminDSN is Pontus's own connection string for this backend.
	//
	// Client sessions forward the client's credentials, so the proxy has no
	// session of its own to run administrative statements in — health probes,
	// role detection, replication lag and slot management all need one. Without
	// it those fall back to running on whichever pooled connection a client
	// last authenticated, which is fragile and runs as that client's user.
	//
	// Give it the least privilege that works: CONNECT, pg_monitor for the
	// statistics views, and REPLICATION only if slot management is wanted.
	AdminDSN string `json:"admin_dsn,omitzero" yaml:"admin_dsn"`
}
