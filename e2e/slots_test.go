//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Creating a logical slot and seeing it in the inventory is the round trip an
// operator performs before pointing a consumer at the proxy.
func TestCreateAndListLogicalSlot(t *testing.T) {
	s := startStack(t)
	token := s.login()
	projectID, proxyID := s.project(token)

	slot := fmt.Sprintf("pontus_e2e_%d", time.Now().UnixNano())

	out, code := s.rpc("CreateLogicalSlot", map[string]string{
		"projectId": projectID,
		"proxyId":   proxyID,
		"slotName":  slot,
		"plugin":    "pgoutput",
	}, token)
	t.Cleanup(func() { dropSlot(t, slot) })

	if code != http.StatusOK {
		message := fmt.Sprint(out["message"])
		// Logical decoding is a server setting, not something the proxy can
		// arrange. Skip rather than fail: the backend is simply not configured
		// for it. Start PostgreSQL with -c wal_level=logical to run this.
		if strings.Contains(message, "wal_level") {
			t.Skipf("backend is not configured for logical decoding: %s", message)
		}
		t.Fatalf("CreateLogicalSlot failed (%d): %v", code, out)
	}

	// The inventory is read from the primary, so the slot must appear there.
	var found bool
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !found {
		listed, code := s.rpc("ListReplicationStreams",
			map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
		if code == http.StatusOK {
			for _, raw := range asSlice(listed["slots"]) {
				entry, ok := raw.(map[string]any)
				if !ok || entry["name"] != slot {
					continue
				}
				found = true
				if entry["slotType"] != "logical" {
					t.Errorf("slot type = %v, want logical", entry["slotType"])
				}
				break
			}
		}
		if !found {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if !found {
		t.Errorf("slot %s was created but does not appear in the inventory", slot)
	}
}

// A slot name PostgreSQL would not accept must be rejected before it reaches
// the database, because the statement is assembled as text — the simple query
// protocol has no bind parameters.
func TestCreateLogicalSlotRejectsUnsafeNames(t *testing.T) {
	s := startStack(t)
	token := s.login()
	projectID, proxyID := s.project(token)

	for _, name := range []string{
		"has-dashes",
		"UPPERCASE",
		"with space",
		"quote'injection",
		"semi;colon",
		"drop'); SELECT pg_sleep(30); --",
		strings.Repeat("x", 64),
		"",
	} {
		_, code := s.rpc("CreateLogicalSlot", map[string]string{
			"projectId": projectID,
			"proxyId":   proxyID,
			"slotName":  name,
			"plugin":    "pgoutput",
		}, token)
		if code == http.StatusOK {
			dropSlot(t, name)
			t.Errorf("slot name %q was accepted", name)
		}
	}
}

// An output plugin name is an identifier too, and reaches the same statement.
func TestCreateLogicalSlotRejectsUnsafePlugins(t *testing.T) {
	s := startStack(t)
	token := s.login()
	projectID, proxyID := s.project(token)

	slot := fmt.Sprintf("pontus_e2e_p_%d", time.Now().UnixNano())
	_, code := s.rpc("CreateLogicalSlot", map[string]string{
		"projectId": projectID,
		"proxyId":   proxyID,
		"slotName":  slot,
		"plugin":    "pgoutput'); SELECT pg_sleep(30); --",
	}, token)

	if code == http.StatusOK {
		dropSlot(t, slot)
		t.Error("an injectable output plugin name was accepted")
	}
}

func asSlice(v any) []any {
	out, _ := v.([]any)
	return out
}

// dropSlot removes a slot directly on the backend, so a failed test does not
// leave WAL pinned on the shared database.
func dropSlot(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), backendAddr(), backendDB()))
	if err != nil {
		return
	}
	defer conn.Close(ctx)
	_, _ = conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", name)
}
