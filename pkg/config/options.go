package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Options represents the gateway configuration.
type Options struct {
	ProxyAddr      string        `json:"proxy_addr,omitzero" yaml:"proxy_addr"`
	MgmtAddr       string        `json:"mgmt_addr,omitzero" yaml:"mgmt_addr"`
	LocalZone      string        `json:"local_zone,omitzero" yaml:"local_zone"`
	Balancer       string        `json:"balancer,omitzero" yaml:"balancer"`
	Backends       []Backend     `json:"backends,omitzero" yaml:"backends"`
	Protocol       string        `json:"protocol,omitzero" yaml:"protocol"`
	DialTimeout    time.Duration `json:"dial_timeout,omitzero" yaml:"dial_timeout"`
	MaxConns       int32         `json:"max_conns,omitzero" yaml:"max_conns"`
	MinIdle        int32         `json:"min_idle,omitzero" yaml:"min_idle"`
	HealthInterval time.Duration `json:"health_interval,omitzero" yaml:"health_interval"`
	TLS            *TLS          `json:"tls,omitzero" yaml:"tls"`
	BackendTLS     *TLS          `json:"backend_tls,omitzero" yaml:"backend_tls"`
	// AgentTLS secures the sidecar connection. Deliberately separate from
	// BackendTLS: the agent and the database are different peers with different
	// names and usually different CAs, and sharing one config is what made this
	// look configured when it was not.
	AgentTLS     *TLS          `json:"agent_tls,omitzero" yaml:"agent_tls"`
	RateLimit    *RateLimit    `json:"rate_limit,omitzero" yaml:"rate_limit"`
	Cache        *Cache        `json:"cache,omitzero" yaml:"cache"`
	Failover     *Failover     `json:"failover,omitzero" yaml:"failover"`
	QueryTimeout time.Duration `json:"query_timeout,omitzero" yaml:"query_timeout"`
	// SlowQueryThreshold is the duration above which a query is logged
	// individually. Below it, queries are recorded by the tracker and logged
	// only at debug level: one INFO line per query with its full SQL text is
	// the dominant log volume on a busy proxy and drowns the events worth
	// reading. Zero takes the default.
	SlowQueryThreshold time.Duration `json:"slow_query_threshold,omitzero" yaml:"slow_query_threshold"`
	PoolingMode        string        `json:"pooling_mode,omitzero" yaml:"pooling_mode"` // "transaction" or "statement"
	ShadowBackends     []Backend     `json:"shadow_backends,omitzero" yaml:"shadow_backends"`
	AdminToken         string        `json:"admin_token,omitzero" yaml:"admin_token"`
	// JWTSecret keys the management session tokens. The name is kept for
	// config compatibility; tokens are PASETO v4.local, not JWT. Prefer the
	// auth_key alias below. There is no default — startup fails without one.
	JWTSecret string `json:"jwt_secret,omitzero" yaml:"jwt_secret"`
	AuthKey   string `json:"auth_key,omitzero" yaml:"auth_key"`
	// AllowedOrigins lists browser origins permitted to call the management
	// API cross-origin. Empty means same-origin only, which is correct when
	// the dashboard is served by this binary. "*" is rejected at startup.
	AllowedOrigins []string `json:"allowed_origins,omitzero" yaml:"allowed_origins"`
	DataDir        string   `json:"data_dir,omitzero" yaml:"data_dir"`
}

// resolveSecrets applies the auth_key alias and the environment overrides.
//
// Secrets belong in the environment more often than in a checked-in config
// file, so PONTUS_AUTH_KEY and PONTUS_ADMIN_TOKEN take precedence.
func (c *Options) resolveSecrets() {
	if c.AuthKey != "" && c.JWTSecret == "" {
		c.JWTSecret = c.AuthKey
	}
	if v := os.Getenv("PONTUS_AUTH_KEY"); v != "" {
		c.JWTSecret = v
	}
	if v := os.Getenv("PONTUS_ADMIN_TOKEN"); v != "" {
		c.AdminToken = v
	}
}

// Load loads the configuration from a YAML file.
func Load(path string) (*Options, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Options{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	cfg.resolveSecrets()
	return cfg, nil
}

// Merge merges non-zero values from other into c.
func (c *Options) Merge(other *Options) {
	if other == nil {
		return
	}

	if other.ProxyAddr != "" {
		c.ProxyAddr = other.ProxyAddr
	}
	if other.MgmtAddr != "" {
		c.MgmtAddr = other.MgmtAddr
	}
	if other.LocalZone != "" {
		c.LocalZone = other.LocalZone
	}
	if other.Balancer != "" {
		c.Balancer = other.Balancer
	}
	if len(other.Backends) > 0 {
		c.Backends = other.Backends
	}
	if other.Protocol != "" {
		c.Protocol = other.Protocol
	}
	if other.DialTimeout > 0 {
		c.DialTimeout = other.DialTimeout
	}
	if other.MaxConns > 0 {
		c.MaxConns = other.MaxConns
	}
	if other.MinIdle > 0 {
		c.MinIdle = other.MinIdle
	}
	if other.HealthInterval > 0 {
		c.HealthInterval = other.HealthInterval
	}
	if other.TLS != nil {
		c.TLS = other.TLS
	}
	if other.BackendTLS != nil {
		c.BackendTLS = other.BackendTLS
	}
	if other.AgentTLS != nil {
		c.AgentTLS = other.AgentTLS
	}
	if other.RateLimit != nil {
		c.RateLimit = other.RateLimit
	}
	if other.Cache != nil {
		c.Cache = other.Cache
	}
	if other.Failover != nil {
		c.Failover = other.Failover
	}
	if other.SlowQueryThreshold > 0 {
		c.SlowQueryThreshold = other.SlowQueryThreshold
	}
	if other.QueryTimeout > 0 {
		c.QueryTimeout = other.QueryTimeout
	}
	if other.PoolingMode != "" {
		c.PoolingMode = other.PoolingMode
	}
	if len(other.ShadowBackends) > 0 {
		c.ShadowBackends = other.ShadowBackends
	}
	if other.AdminToken != "" {
		c.AdminToken = other.AdminToken
	}
	if other.JWTSecret != "" {
		c.JWTSecret = other.JWTSecret
	}
	if other.DataDir != "" {
		c.DataDir = other.DataDir
	}
}
