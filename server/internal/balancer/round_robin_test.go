package balancer

import (
	"context"
	"testing"

	"github.com/gsoultan/pontus/server/internal/pool"
)

func TestRoundRobin_Next(t *testing.T) {
	b1 := &mockBackend{address: "1", healthy: true, role: pool.RoleReplica}
	b2 := &mockBackend{address: "2", healthy: true, role: pool.RoleReplica}
	b3 := &mockBackend{address: "3", healthy: false, role: pool.RoleReplica}

	rb := NewRoundRobin([]pool.Backend{b1, b2, b3})

	// First call (Read)
	res, err := rb.Next(t.Context(), Hint{ReadOnly: true})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if res.Address() != "2" {
		t.Errorf("Expected address 2, got %s", res.Address())
	}

	// Second call (Read)
	res, err = rb.Next(context.Background(), Hint{ReadOnly: true})
	if res.Address() != "1" {
		t.Errorf("Expected address 1, got %s", res.Address())
	}
}

func TestRoundRobin_ReadWriteSplitting(t *testing.T) {
	primary := &mockBackend{address: "primary", healthy: true, role: pool.RolePrimary}
	replica := &mockBackend{address: "replica", healthy: true, role: pool.RoleReplica}

	rb := NewRoundRobin([]pool.Backend{primary, replica})

	// Write query (Hint{ReadOnly: false}) -> must go to primary
	res, err := rb.Next(context.Background(), Hint{ReadOnly: false})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if res.Address() != "primary" {
		t.Errorf("Expected primary, got %s", res.Address())
	}

	// Read query (Hint{ReadOnly: true}) -> should go to replica
	res, err = rb.Next(context.Background(), Hint{ReadOnly: true})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if res.Address() != "replica" {
		t.Errorf("Expected replica, got %s", res.Address())
	}
}

func TestRoundRobin_ReadFallback(t *testing.T) {
	primary := &mockBackend{address: "primary", healthy: true, role: pool.RolePrimary}
	replica := &mockBackend{address: "replica", healthy: false, role: pool.RoleReplica}

	rb := NewRoundRobin([]pool.Backend{primary, replica})

	// Read query with unhealthy replica -> fallback to primary
	res, err := rb.Next(t.Context(), Hint{ReadOnly: true})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if res.Address() != "primary" {
		t.Errorf("Expected fallback to primary, got %s", res.Address())
	}
}
