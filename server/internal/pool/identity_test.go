package pool

import "testing"

// A connection carries the credentials it authenticated with and cannot
// renegotiate them. Handing one authenticated as alice to a session for bob
// would run bob's queries with alice's privileges.
func TestConnBelongsToOnlyItsOwnIdentity(t *testing.T) {
	c := &Conn{}
	c.SetIdentity("alice", "sales")

	if !c.BelongsTo("alice", "sales") {
		t.Error("a connection refused the identity it authenticated as")
	}
	if c.BelongsTo("bob", "sales") {
		t.Error("alice's connection was offered to bob")
	}
	if c.BelongsTo("alice", "hr") {
		t.Error("a connection to one database was offered for another")
	}
	if c.BelongsTo("", "") {
		t.Error("an authenticated connection was treated as anonymous")
	}
}

// A connection that has not completed a startup exchange belongs to nobody yet,
// and is exactly what the next handshake needs — that handshake is what gives
// it an identity.
func TestUnauthenticatedConnBelongsToAnyone(t *testing.T) {
	c := &Conn{}
	if !c.BelongsTo("alice", "sales") {
		t.Error("a fresh connection was refused to the handshake that would authenticate it")
	}

	user, database := c.Identity()
	if user != "" || database != "" {
		t.Errorf("fresh connection reported identity %q/%q", user, database)
	}
}

func TestIdentityIsReadBack(t *testing.T) {
	c := &Conn{}
	c.SetIdentity("carol", "analytics")

	user, database := c.Identity()
	if user != "carol" || database != "analytics" {
		t.Errorf("Identity() = %q/%q, want carol/analytics", user, database)
	}
}
