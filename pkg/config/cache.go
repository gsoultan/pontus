package config

import "time"

// Cache represents query cache settings.
type Cache struct {
	Enabled bool          `json:"enabled,omitzero" yaml:"enabled"`
	TTL     time.Duration `json:"ttl,omitzero" yaml:"ttl"`
	MaxSize int           `json:"max_size,omitzero" yaml:"max_size"`

	// MaxEntrySize bounds a single reply, in bytes, both for what the cache
	// will store and for what request collapsing will share.
	//
	// It exists because a reply is streamed to the client but *held* by the
	// proxy while it is being captured: without a bound, one
	// `SELECT * FROM events` buffers the whole result set in Pontus's heap,
	// per concurrent client, on the way to a cache that would not have kept
	// something that size anyway.
	//
	// Passing it is not an error. The client is still streamed every byte —
	// only the remembering stops, so the reply is neither cached nor shared
	// with collapsed requests. Unset means 8 MiB.
	MaxEntrySize int `json:"max_entry_size,omitzero" yaml:"max_entry_size"`
}
