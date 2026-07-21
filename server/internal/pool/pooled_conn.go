package pool

import (
	"net"
	"time"
)

type pooledConn struct {
	conn      net.Conn
	at        time.Time
	createdAt time.Time
}
