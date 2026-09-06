package pool

// SetPeerAddress records how *other database nodes* reach this one.
//
// Rebuilding a node runs pg_basebackup on that node against its new primary,
// and there it is the node's view of the address that matters, not the proxy's.
// The two are the same on a flat network and differ behind NAT or in
// containers, where a proxy that reaches a primary on 127.0.0.1 would be
// telling a peer to connect to itself. Patroni calls this connect_address.
func (p *Server) SetPeerAddress(addr string) {
	p.mu.Lock()
	p.peerAddr = addr
	p.mu.Unlock()
}

// PeerAddress is the address peers should use, falling back to the proxy's.
//
// The fallback is the common case and must stay silent: on a flat network the
// two are identical, and requiring an operator to state that would make every
// ordinary deployment configure something it does not need.
func (p *Server) PeerAddress() string {
	p.mu.RLock()
	peer := p.peerAddr
	p.mu.RUnlock()

	if peer != "" {
		return peer
	}
	return p.Address()
}

// SetDataDirectory records where this node's PostgreSQL cluster lives.
//
// Rebuilding a node erases its data directory, which makes this the one place a
// guess must not decide the answer. A scan of the usual locations fails badly
// rather than obviously on a host running two clusters: it finds *a* cluster.
func (p *Server) SetDataDirectory(dir string) {
	p.mu.Lock()
	p.dataDir = dir
	p.mu.Unlock()
}

// DataDirectory is the configured data directory, or empty when none was given.
func (p *Server) DataDirectory() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dataDir
}
