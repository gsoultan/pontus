package pool

import (
	"net"
	"time"
)

type Conn struct {
	net.Conn
	createdAt time.Time
	lastUsed  time.Time
	useCount  int64
}

func NewConn(conn net.Conn) *Conn {
	now := time.Now()
	return &Conn{
		Conn:      conn,
		createdAt: now,
		lastUsed:  now,
	}
}

func (c *Conn) CreatedAt() time.Time {
	return c.createdAt
}

func (c *Conn) LastUsed() time.Time {
	return c.lastUsed
}

func (c *Conn) UseCount() int64 {
	return c.useCount
}

func (c *Conn) IncUseCount() {
	c.useCount++
	c.lastUsed = time.Now()
}
