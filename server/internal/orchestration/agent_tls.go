package orchestration

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	agentTLSMu     sync.RWMutex
	agentTLS       *tls.Config
	allowCleartext bool
	warnedPlain    sync.Once
)

// ErrCleartextAgent reports a remote agent that would be reached without
// encryption.
var ErrCleartextAgent = errors.New("agent connection would be unencrypted")

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

// SetAllowCleartextAgents permits reaching a remote agent without encryption.
//
// An explicit decision, because the default is now to refuse. An operator who
// has decided their agent network is trusted can say so; an operator who has
// simply not configured TLS should find out at startup rather than after a
// token has been read off the wire.
func SetAllowCleartextAgents(allow bool) {
	agentTLSMu.Lock()
	defer agentTLSMu.Unlock()
	allowCleartext = allow
}

func cleartextAllowed() bool {
	agentTLSMu.RLock()
	defer agentTLSMu.RUnlock()
	return allowCleartext
}

// checkTransport refuses to carry the agent token in cleartext to a peer that
// is not on this host.
//
// The token is a bearer credential for an interface that rebuilds nodes, takes
// backups and deletes data directories, as root. Warning and connecting anyway
// — which is what this did — is the "auth that no-ops" shape the security
// profile exists to refuse. It was defensible while every one of those
// operations was a stub; it stopped being defensible when they started doing
// what they claim.
//
// Loopback is exempt because nothing crosses a network: the token never leaves
// the machine that already runs both processes.
func checkTransport(addr string) error {
	if AgentTLS() != nil {
		return nil
	}
	if isLoopbackAddr(addr) {
		return nil
	}
	if cleartextAllowed() {
		warnIfCleartext(addr)
		return nil
	}

	return fmt.Errorf("%w: %s is not on this host and agent_tls is not configured. "+
		"The agent token authorises rebuilding nodes, taking backups and deleting "+
		"data directories as root, and would be sent in cleartext. Configure "+
		"agent_tls (and start the agent with -tls-cert/-tls-key), or set "+
		"agent_allow_cleartext: true to accept the risk deliberately",
		ErrCleartextAgent, addr)
}

// isLoopbackAddr reports whether an address names this host.
//
// A name that does not resolve is not loopback: the question is whether the
// token stays on this machine, and an unanswerable question has to be answered
// no.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
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
