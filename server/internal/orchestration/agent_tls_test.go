package orchestration

import (
	"crypto/tls"
	"errors"
	"strings"
	"testing"
)

// resetTransportPolicy restores the package state these tests mutate, so one
// case cannot decide another's outcome.
func resetTransportPolicy(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		SetAgentTLS(nil)
		SetAllowCleartextAgents(false)
	})
	SetAgentTLS(nil)
	SetAllowCleartextAgents(false)
}

// The agent token authorises rebuilding a node and deleting a data directory as
// root. Carrying it to another host in cleartext has to be refused, not warned
// about — a warning in a log nobody reads is not a control.
func TestRemoteAgentWithoutTLSIsRefused(t *testing.T) {
	resetTransportPolicy(t)

	err := checkTransport("10.0.0.5:9091")
	if err == nil {
		t.Fatal("a remote agent was accepted without encryption")
	}
	if !errors.Is(err, ErrCleartextAgent) {
		t.Errorf("error = %v, want ErrCleartextAgent", err)
	}
	// The message has to say what to do, since this fails a working
	// configuration on upgrade.
	for _, want := range []string{"agent_tls", "agent_allow_cleartext"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// Loopback is exempt because nothing crosses a network: the token never leaves
// the machine that already runs both processes.
func TestLoopbackAgentNeedsNoEncryption(t *testing.T) {
	resetTransportPolicy(t)

	for _, addr := range []string{
		"127.0.0.1:9091",
		"localhost:9091",
		"[::1]:9091",
		"127.0.0.5:9091",
	} {
		if err := checkTransport(addr); err != nil {
			t.Errorf("checkTransport(%q) = %v, want nil", addr, err)
		}
	}
}

func TestTLSMakesAnyAgentAcceptable(t *testing.T) {
	resetTransportPolicy(t)
	SetAgentTLS(&tls.Config{MinVersion: tls.VersionTLS12})

	if err := checkTransport("10.0.0.5:9091"); err != nil {
		t.Errorf("a remote agent with TLS was refused: %v", err)
	}
}

// An operator who has decided their agent network is trusted can say so. One
// who has simply not configured TLS should find out at startup.
func TestCleartextCanBeAcceptedDeliberately(t *testing.T) {
	resetTransportPolicy(t)
	SetAllowCleartextAgents(true)

	if err := checkTransport("10.0.0.5:9091"); err != nil {
		t.Errorf("an explicitly accepted cleartext agent was refused: %v", err)
	}
}

// A name that does not resolve is not loopback: the question is whether the
// token stays on this machine, and an unanswerable question has to be answered
// no.
func TestUnresolvableHostIsNotLoopback(t *testing.T) {
	resetTransportPolicy(t)

	for _, addr := range []string{
		"db1.internal:9091",
		"10.0.0.5:9091",
		"[fe80::1]:9091",
		"",
	} {
		if isLoopbackAddr(addr) {
			t.Errorf("isLoopbackAddr(%q) = true", addr)
		}
	}
}

// NewAgentClient is where the policy has to bite: every provisioning call goes
// through it.
func TestNewAgentClientRefusesCleartextToARemoteHost(t *testing.T) {
	resetTransportPolicy(t)

	if _, err := NewAgentClient("10.0.0.5:9091", "a-token"); !errors.Is(err, ErrCleartextAgent) {
		t.Errorf("NewAgentClient error = %v, want ErrCleartextAgent", err)
	}

	// Loopback still connects, which is what the e2e harness and a
	// single-host deployment rely on.
	client, err := NewAgentClient("127.0.0.1:9091", "a-token")
	if err != nil {
		t.Fatalf("a loopback agent was refused: %v", err)
	}
	_ = client.Close()
}
