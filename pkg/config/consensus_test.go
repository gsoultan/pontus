package config

import (
	"errors"
	"strings"
	"testing"
)

// Every one of these failures shows up later as "no leader", and a cluster that
// never elects one looks exactly like a cluster still waiting to. The
// difference is only visible in config, so it is checked there.
func TestConsensusValidateRefusesWhatCannotElect(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Consensus
		want string
	}{
		{
			"no node id",
			&Consensus{Enabled: true, BindAddr: "10.0.0.1:9095"},
			"node_id is required",
		},
		{
			"no bind address",
			&Consensus{Enabled: true, NodeID: "a"},
			"bind_addr is required",
		},
		{
			"bind address is not host:port",
			&Consensus{Enabled: true, NodeID: "a", BindAddr: "10.0.0.1"},
			"not host:port",
		},
		{
			"a peer with no address",
			&Consensus{Enabled: true, NodeID: "a", BindAddr: "10.0.0.1:9095",
				Peers: []ConsensusPeer{{NodeID: "b"}}},
			"every peer needs",
		},
		{
			"a peer address that is not host:port",
			&Consensus{Enabled: true, NodeID: "a", BindAddr: "10.0.0.1:9095",
				Peers: []ConsensusPeer{{NodeID: "b", Addr: "10.0.0.2"}}},
			"not host:port",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatal("accepted")
			}
			if !errors.Is(err, ErrConsensusIncomplete) {
				t.Errorf("error = %v, want ErrConsensusIncomplete", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// Raft records votes against a node id. Two nodes sharing one can each vote in
// the same term, which elects two leaders — the thing consensus exists to stop,
// arranged in config.
func TestConsensusRefusesADuplicateNodeID(t *testing.T) {
	dupPeer := &Consensus{
		Enabled: true, NodeID: "a", BindAddr: "10.0.0.1:9095",
		Peers: []ConsensusPeer{
			{NodeID: "b", Addr: "10.0.0.2:9095"},
			{NodeID: "b", Addr: "10.0.0.3:9095"},
		},
	}
	if err := dupPeer.Validate(); err == nil {
		t.Error("two peers sharing a node_id were accepted")
	}

	// Including a peer that repeats this node's own id.
	selfPeer := &Consensus{
		Enabled: true, NodeID: "a", BindAddr: "10.0.0.1:9095",
		Peers: []ConsensusPeer{{NodeID: "a", Addr: "10.0.0.2:9095"}},
	}
	if err := selfPeer.Validate(); err == nil {
		t.Error("a peer sharing this node's id was accepted")
	}
}

// Off is the default and must stay cheap to leave alone.
func TestConsensusValidatesOnlyWhenEnabled(t *testing.T) {
	if err := (*Consensus)(nil).Validate(); err != nil {
		t.Errorf("an absent block failed validation: %v", err)
	}
	// Disabled but half-filled — an operator part-way through setting it up.
	if err := (&Consensus{NodeID: "a"}).Validate(); err != nil {
		t.Errorf("a disabled block failed validation: %v", err)
	}

	valid := &Consensus{
		Enabled: true, NodeID: "a", BindAddr: "10.0.0.1:9095", Bootstrap: true,
		Peers: []ConsensusPeer{{NodeID: "b", Addr: "10.0.0.2:9095"}},
	}
	if err := valid.Validate(); err != nil {
		t.Errorf("a valid block was refused: %v", err)
	}
}

func TestConsensusEnabledReadsTheBlock(t *testing.T) {
	if (&Options{}).ConsensusEnabled() {
		t.Error("consensus is on with no block")
	}
	if (&Options{Consensus: &Consensus{}}).ConsensusEnabled() {
		t.Error("consensus is on with enabled unset")
	}
	if !(&Options{Consensus: &Consensus{Enabled: true}}).ConsensusEnabled() {
		t.Error("consensus is off with enabled set")
	}
}
