package pool

import "sync/atomic"

// DatabaseLimits answers the per-identity ceiling for a database, or zero when
// that database has no rule of its own and takes the global `max_conns`.
//
// A function rather than the configuration type so the pool does not have to
// import `pkg/config`, which would point a data-plane package at the control
// plane's vocabulary for no gain.
type DatabaseLimits func(database string) int32

// SetDatabaseLimits installs the per-database ceilings.
//
// Swapped whole through an atomic pointer rather than mutated, because it is
// read on the acquire path when a pool is first created and replaced on a
// configuration reload.
func (p *Server) SetDatabaseLimits(limits DatabaseLimits) {
	if limits == nil {
		p.databaseLimits.Store(nil)
		return
	}
	p.databaseLimits.Store(&limits)
}

// limitFor is the ceiling for one identity: the per-database rule if it has
// one, otherwise zero meaning "no opinion".
func (p *Server) limitFor(database string) int32 {
	limits := p.databaseLimits.Load()
	if limits == nil {
		return 0
	}
	return (*limits)(database)
}

// ceilingFor is the ceiling a pool for this identity should actually run at.
//
// The per-database rule is a **cap**, not a target. The adaptive controller
// lowers capacity for the whole backend under pressure, and a per-database
// value that overrode it would let one tenant keep the connections the
// controller is trying to give back. The lower of the two is the only reading
// that respects both.
func ceilingFor(configured, limit int32) int32 {
	if limit > 0 && limit < configured {
		return limit
	}
	return configured
}

// databaseLimitStore is the field type; declared here so the Server struct in
// server.go carries nothing this file does not explain.
type databaseLimitStore = atomic.Pointer[DatabaseLimits]
