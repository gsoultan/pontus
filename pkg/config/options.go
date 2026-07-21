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
	Firewall       *Firewall     `json:"firewall,omitzero" yaml:"firewall"`
	RateLimit      *RateLimit    `json:"rate_limit,omitzero" yaml:"rate_limit"`
	Cache          *Cache        `json:"cache,omitzero" yaml:"cache"`
	QueryTimeout   time.Duration `json:"query_timeout,omitzero" yaml:"query_timeout"`
	PoolingMode    string        `json:"pooling_mode,omitzero" yaml:"pooling_mode"` // "transaction" or "statement"
	ShadowBackends []Backend     `json:"shadow_backends,omitzero" yaml:"shadow_backends"`
	AdminToken     string        `json:"admin_token,omitzero" yaml:"admin_token"`
	JWTSecret      string        `json:"jwt_secret,omitzero" yaml:"jwt_secret"`
	DataDir        string        `json:"data_dir,omitzero" yaml:"data_dir"`
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
	if other.Firewall != nil {
		c.Firewall = other.Firewall
	}
	if other.RateLimit != nil {
		c.RateLimit = other.RateLimit
	}
	if other.Cache != nil {
		c.Cache = other.Cache
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
