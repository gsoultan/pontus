package proxy

import (
	"encoding/binary"
	"testing"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

func startupPacketFor(user, database string) []byte {
	body := binary.BigEndian.AppendUint32(nil, 196608)
	for _, p := range []string{"user", user, "database", database} {
		body = append(body, p...)
		body = append(body, 0)
	}
	body = append(body, 0)

	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))
	return append(out, body...)
}

func databaseIn(t *testing.T, raw []byte) string {
	t.Helper()

	payload := raw[8:]
	for {
		idx := indexOfZero(payload)
		if idx <= 0 {
			return ""
		}
		key := string(payload[:idx])
		payload = payload[idx+1:]

		idx = indexOfZero(payload)
		if idx < 0 {
			return ""
		}
		value := string(payload[:idx])
		payload = payload[idx+1:]

		if key == "database" {
			return value
		}
	}
}

func indexOfZero(b []byte) int {
	for i := range b {
		if b[i] == 0 {
			return i
		}
	}
	return -1
}

// The parsed field, the session state and the raw packet must all agree. The
// pool is keyed by the database, the backend startup names it, and the
// connection records it for the identity check on reuse — if those disagree, a
// connection is filed under one name and opened against another.
func TestResolveDatabaseRewritesEverySite(t *testing.T) {
	routes := config.Databases{{Name: "app", Database: "app_prod"}}

	req := &protocol.StartupRequest{
		Raw:      startupPacketFor("app_user", "app"),
		User:     "app_user",
		Database: "app",
	}
	state := &protocol.SessionState{User: "app_user", Database: "app"}

	if err := resolveDatabase(routes, req, state); err != nil {
		t.Fatalf("resolveDatabase: %v", err)
	}

	if req.Database != "app_prod" {
		t.Errorf("req.Database = %q, want app_prod", req.Database)
	}
	if state.Database != "app_prod" {
		t.Errorf("state.Database = %q, want app_prod", state.Database)
	}
	// The raw packet is what passthrough forwards to the backend.
	if got := databaseIn(t, req.Raw); got != "app_prod" {
		t.Errorf("raw packet database = %q, want app_prod", got)
	}
}

// An unlisted database is left completely alone, including its packet — an
// untouched byte slice is the cheapest proof that the common path costs
// nothing.
func TestResolveDatabaseLeavesAnUnlistedNameAlone(t *testing.T) {
	routes := config.Databases{{Name: "app", Database: "app_prod"}}

	raw := startupPacketFor("app_user", "reporting")
	req := &protocol.StartupRequest{Raw: raw, User: "app_user", Database: "reporting"}
	state := &protocol.SessionState{User: "app_user", Database: "reporting"}

	if err := resolveDatabase(routes, req, state); err != nil {
		t.Fatalf("resolveDatabase: %v", err)
	}

	if req.Database != "reporting" || state.Database != "reporting" {
		t.Errorf("an unlisted database was rewritten to %q/%q", req.Database, state.Database)
	}
	if &raw[0] != &req.Raw[0] {
		t.Error("the packet was rebuilt for a database that needed no rewrite")
	}
}

// A rule that carries only a limit does not rename anything.
func TestResolveDatabaseDoesNotRewriteALimitOnlyRule(t *testing.T) {
	routes := config.Databases{{Name: "app", MaxConns: 5}}

	raw := startupPacketFor("app_user", "app")
	req := &protocol.StartupRequest{Raw: raw, User: "app_user", Database: "app"}
	state := &protocol.SessionState{User: "app_user", Database: "app"}

	if err := resolveDatabase(routes, req, state); err != nil {
		t.Fatalf("resolveDatabase: %v", err)
	}
	if req.Database != "app" {
		t.Errorf("req.Database = %q, want app", req.Database)
	}
	if &raw[0] != &req.Raw[0] {
		t.Error("the packet was rebuilt for a rule that only sets a limit")
	}
}

// With no routing table configured the function must not touch anything, which
// is the default every existing deployment runs.
func TestResolveDatabaseIsANoOpWithoutRoutes(t *testing.T) {
	raw := startupPacketFor("app_user", "app")
	req := &protocol.StartupRequest{Raw: raw, User: "app_user", Database: "app"}
	state := &protocol.SessionState{User: "app_user", Database: "app"}

	if err := resolveDatabase(nil, req, state); err != nil {
		t.Fatalf("resolveDatabase: %v", err)
	}
	if req.Database != "app" || state.Database != "app" {
		t.Error("an empty routing table changed the database")
	}
}
