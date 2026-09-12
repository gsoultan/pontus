package protocol

import (
	"encoding/binary"
	"testing"
)

// buildStartup assembles a StartupMessage the way a client sends one.
func buildStartup(params ...string) []byte {
	body := binary.BigEndian.AppendUint32(nil, 196608) // protocol 3.0
	for _, p := range params {
		body = append(body, p...)
		body = append(body, 0)
	}
	body = append(body, 0)

	out := binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))
	return append(out, body...)
}

// readStartup parses one back, so a rewrite is checked by reading it the way
// the backend will rather than by comparing bytes we produced ourselves.
func readStartup(t *testing.T, raw []byte) map[string]string {
	t.Helper()

	if got, want := int(binary.BigEndian.Uint32(raw[:4])), len(raw); got != want {
		t.Fatalf("length prefix says %d, packet is %d bytes", got, want)
	}
	if got := binary.BigEndian.Uint32(raw[4:8]); got != 196608 {
		t.Fatalf("protocol version = %d, want 196608", got)
	}

	out := map[string]string{}
	payload := raw[8:]
	for {
		idx := indexZero(payload)
		if idx <= 0 {
			return out
		}
		key := string(payload[:idx])
		payload = payload[idx+1:]

		idx = indexZero(payload)
		if idx < 0 {
			t.Fatalf("parameter %q has no value", key)
		}
		out[key] = string(payload[:idx])
		payload = payload[idx+1:]
	}
}

// The rewrite has to reach the backend, because passthrough forwards the
// client's own packet. Rewriting only the parsed field would key the pool by
// the alias while the connection opened the name the client sent.
func TestRewriteStartupDatabaseReplacesTheValue(t *testing.T) {
	raw := buildStartup(
		"user", "app_user",
		"database", "app",
		"application_name", "psql",
	)

	out, err := RewriteStartupDatabase(raw, "app_prod")
	if err != nil {
		t.Fatalf("RewriteStartupDatabase: %v", err)
	}

	params := readStartup(t, out)
	if params["database"] != "app_prod" {
		t.Errorf("database = %q, want app_prod", params["database"])
	}
	// Everything else survives, including the parameters after the one that
	// changed — a rewrite that truncated the tail would drop application_name.
	if params["user"] != "app_user" {
		t.Errorf("user = %q, want app_user", params["user"])
	}
	if params["application_name"] != "psql" {
		t.Errorf("application_name = %q, want psql", params["application_name"])
	}
	if got, want := len(params), 3; got != want {
		t.Errorf("got %d parameters, want %d: %v", got, want, params)
	}
}

// A longer name changes the packet's length, and the prefix covers itself.
func TestRewriteStartupDatabaseFixesTheLengthPrefix(t *testing.T) {
	for _, target := range []string{"a", "a_much_longer_database_name_than_the_original"} {
		raw := buildStartup("user", "u", "database", "original")

		out, err := RewriteStartupDatabase(raw, target)
		if err != nil {
			t.Fatalf("RewriteStartupDatabase(%q): %v", target, err)
		}

		// readStartup checks the prefix against the real length.
		if got := readStartup(t, out)["database"]; got != target {
			t.Errorf("database = %q, want %q", got, target)
		}
	}
}

// A client may omit "database" entirely; PostgreSQL then defaults it to the
// user name. An alias still has to be honoured, so the parameter is added
// rather than the rewrite silently doing nothing.
func TestRewriteStartupDatabaseAddsAMissingParameter(t *testing.T) {
	raw := buildStartup("user", "app_user")

	out, err := RewriteStartupDatabase(raw, "app_prod")
	if err != nil {
		t.Fatalf("RewriteStartupDatabase: %v", err)
	}

	params := readStartup(t, out)
	if params["database"] != "app_prod" {
		t.Errorf("database = %q, want app_prod", params["database"])
	}
	if params["user"] != "app_user" {
		t.Errorf("user = %q, want app_user", params["user"])
	}
}

func TestRewriteStartupDatabaseRefusesAMalformedPacket(t *testing.T) {
	if _, err := RewriteStartupDatabase([]byte{0, 0, 0, 4}, "x"); err == nil {
		t.Error("a packet with no parameters was accepted")
	}

	// A key with no value: the parameter list ends mid-pair.
	truncated := binary.BigEndian.AppendUint32(nil, 196608)
	truncated = append(truncated, "database"...)
	truncated = append(truncated, 0)
	packet := binary.BigEndian.AppendUint32(nil, uint32(len(truncated)+4))
	packet = append(packet, truncated...)

	if _, err := RewriteStartupDatabase(packet, "x"); err == nil {
		t.Error("a parameter with no value was accepted")
	}
}

// The rewritten packet must be one PostgreSQL itself would accept, so round-trip
// it through the parser the proxy uses on the way in.
func TestRewriteStartupDatabaseRoundTripsThroughTheReader(t *testing.T) {
	raw := buildStartup("user", "app_user", "database", "app", "replication", "database")

	out, err := RewriteStartupDatabase(raw, "app_prod")
	if err != nil {
		t.Fatalf("RewriteStartupDatabase: %v", err)
	}

	user, database, replication := extractStartupParams(&startupPacket{raw: out})
	if user != "app_user" {
		t.Errorf("user = %q, want app_user", user)
	}
	if database != "app_prod" {
		t.Errorf("database = %q, want app_prod", database)
	}
	if replication != "database" {
		t.Errorf("replication = %q, want database", replication)
	}
}
