package balancer

import (
	"testing"

	"github.com/gsoultan/pontus/server/internal/pool"
)

func TestLeastConn_Next(t *testing.T) {
	b1 := &mockBackend{address: "1", healthy: true, role: pool.RolePrimary, activeConns: 1}
	b2 := &mockBackend{address: "busy", healthy: true, role: pool.RolePrimary, activeConns: 10}

	lc := NewLeastConn([]pool.Backend{b1, b2})

	res, err := lc.Next(t.Context(), Hint{})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if res.Address() != "1" {
		t.Errorf("Expected address 1 (least conns), got %s", res.Address())
	}
}

func TestLeastConn_ReadWriteSplitting(t *testing.T) {
	primary := &mockBackend{address: "primary", healthy: true, role: pool.RolePrimary}
	replica := &mockBackend{address: "replica", healthy: true, role: pool.RoleReplica}

	lc := NewLeastConn([]pool.Backend{primary, replica})

	// Write
	res, err := lc.Next(t.Context(), Hint{ReadOnly: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.Address() != "primary" {
		t.Errorf("Expected primary, got %s", res.Address())
	}

	// Read
	res, err = lc.Next(t.Context(), Hint{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Address() != "replica" {
		t.Errorf("Expected replica, got %s", res.Address())
	}
}
