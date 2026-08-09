package config

import "time"

// Auth configures how Pontus authenticates clients and opens backend
// connections on their behalf.
//
// Default is passthrough — the original behaviour, where the client's own
// startup exchange is relayed to one backend. That exchange can only happen
// once, on one connection, which is why a session cannot be moved between
// backends and connections cannot be shared. Switching to "pontus" is what
// lifts that, and it is opt-in because it changes client-visible
// authentication and needs a credential source configured.
type Auth struct {
	// Mode is "passthrough" (default) or "pontus".
	Mode string `json:"mode,omitzero" yaml:"mode"`

	// Query reads a role's verifier. Empty takes the built-in query against
	// pg_authid, which needs superuser — see the SECURITY DEFINER recipe in
	// docs/design/backend-auth.md for the deployment that does not.
	Query string `json:"auth_query,omitzero" yaml:"auth_query"`

	// File is a static user→verifier list, used instead of Query.
	File string `json:"auth_file,omitzero" yaml:"auth_file"`

	// CacheTTL is how long a verifier is trusted before it is read again, so a
	// changed password takes effect. Zero takes the default.
	CacheTTL time.Duration `json:"cache_ttl,omitzero" yaml:"cache_ttl"`

	// NegativeCacheTTL is how long an unknown user is remembered. Without this
	// every attempt for a name that does not exist becomes a query against the
	// primary, so a username list turns into database load. Zero takes the
	// default.
	NegativeCacheTTL time.Duration `json:"negative_cache_ttl,omitzero" yaml:"negative_cache_ttl"`

	// CacheSize bounds the cache. Its key is a client-supplied user name, so
	// this is a memory bound rather than a tuning knob. Zero takes the default.
	CacheSize int `json:"cache_size,omitzero" yaml:"cache_size"`
}

// PontusAuth reports whether Pontus should authenticate clients itself.
func (c *Options) PontusAuth() bool {
	return c.Auth != nil && c.Auth.Mode == "pontus"
}

// AuthOptions returns the auth block, never nil.
func (c *Options) AuthOptions() Auth {
	if c.Auth == nil {
		return Auth{Mode: "passthrough"}
	}
	out := *c.Auth
	if out.Mode == "" {
		out.Mode = "passthrough"
	}
	return out
}
