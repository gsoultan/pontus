package orchestration

import (
	"crypto/tls"
	"log/slog"
	"sync"
)

// Agent transport security.
//
// The agent token is mandatory on both ends now, which means it is on the wire
// on every call — and it guards InstallDatabase, PromoteNode and RemoveDatabase
// on a host where the agent runs as root. A bearer token sent in cleartext is a
// bearer token for anyone on the path.
//
// Held here rather than threaded through NewAgentClient because the seven call
// sites are spread across the provisioner and the backend manager, and none of
// them is a sensible place to decide transport security. The registry sets it
// once from configuration.
//
// Deliberately *not* the same tls.Config as the database dialer. They are
// different peers with different names and usually different CAs; sharing one
// was the shortcut that made this look configured when it was not.
var (
	agentTLSMu  sync.RWMutex
	agentTLS    *tls.Config
	warnedPlain sync.Once
)

// SetAgentTLS installs the TLS configuration used for every agent connection.
// A nil config means cleartext, which is warned about once at first use.
func SetAgentTLS(cfg *tls.Config) {
	agentTLSMu.Lock()
	defer agentTLSMu.Unlock()
	agentTLS = cfg
}

// AgentTLS returns the configured client TLS, or nil for cleartext.
func AgentTLS() *tls.Config {
	agentTLSMu.RLock()
	defer agentTLSMu.RUnlock()
	return agentTLS
}

// warnIfCleartext says so once, naming what is exposed. Once, because this is
// on the path of every provisioning call and a per-call warning would be
// scrolled past rather than read.
func warnIfCleartext(addr string) {
	if AgentTLS() != nil {
		return
	}
	warnedPlain.Do(func() {
		slog.Warn("Agent connections are not encrypted; the agent token crosses "+
			"the network in cleartext on every call",
			"example_agent", addr,
			"exposed", "InstallDatabase, PromoteNode, RemoveDatabase",
			"hint", "set agent_tls in the config and start the agent with -tls-cert/-tls-key")
	})
}
