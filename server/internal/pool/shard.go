package pool

import (
	"net"
	"sync"
	"sync/atomic"
)

type shard struct {
	idleConns    chan pooledConn
	waiters      [3]chan net.Conn
	waitersCount [3]atomic.Int64
	activeConns  atomic.Int64
	mu           sync.Mutex
	cond         *sync.Cond
}
