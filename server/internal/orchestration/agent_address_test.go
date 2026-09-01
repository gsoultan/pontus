package orchestration

import (
	"testing"

	"github.com/gsoultan/pontus/server/internal/pool"
)

type addrBackend struct {
	pool.Backend
	agentAddr string
}

func (b *addrBackend) AgentAddr() string { return b.agentAddr }

// agent_addr is mandatory, validated at startup, and honoured by the pool's own
// health and lag checks — and every orchestration call site used to build
// "<database host>:9091" and ignore it, while taking the *token* from the very
// same backend. Point an agent anywhere but the database host's default port
// and health checks kept working while failover could not promote anything.
func TestAgentAddressPrefersTheConfiguredOne(t *testing.T) {
	for name, tc := range map[string]struct {
		backend pool.Backend
		host    string
		want    string
	}{
		"a configured address wins": {
			backend: &addrBackend{agentAddr: "10.0.0.5:9200"},
			host:    "192.168.1.10",
			want:    "10.0.0.5:9200",
		},
		"a non-default port on the same host is honoured": {
			backend: &addrBackend{agentAddr: "127.0.0.1:19093"},
			host:    "127.0.0.1",
			want:    "127.0.0.1:19093",
		},
		"an unset address falls back to the agent's default port": {
			backend: &addrBackend{agentAddr: ""},
			host:    "db1.internal",
			want:    "db1.internal:9091",
		},
		"a nil backend falls back too": {
			backend: nil,
			host:    "db2.internal",
			want:    "db2.internal:9091",
		},
		"an IPv6 host is bracketed, not concatenated": {
			backend: &addrBackend{agentAddr: ""},
			host:    "::1",
			want:    "[::1]:9091",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := agentAddressFor(tc.backend, tc.host); got != tc.want {
				t.Errorf("agentAddressFor = %q, want %q", got, tc.want)
			}
		})
	}
}
