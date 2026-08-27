package middleware

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/server/internal/cache"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

// simpleQuery builds a cacheable 'Q' message.
func simpleQuery(sql string) []byte {
	length := 4 + len(sql) + 1
	out := []byte{'Q', byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
	out = append(out, sql...)
	return append(out, 0)
}

// discardConn accepts writes and reports a stable address, standing in for the
// client socket.
type discardConn struct{ net.Conn }

func (discardConn) Write(p []byte) (int, error) { return len(p), nil }
func (discardConn) Close() error                { return nil }

func cacheSession(sql string, txState protocol.TransactionState) *Session {
	return &Session{
		Data:       simpleQuery(sql),
		Normalized: sql,
		Client:     discardConn{},
		QueryInfo:  protocol.QueryInfo{ReadOnly: true},
		State: &protocol.SessionState{
			User:     "alice",
			Database: "shop",
			TxState:  txState,
		},
	}
}

// The cache stored whatever the backend replied, and an ErrorResponse is a
// perfectly normal reply — `err` from the chain is a *transport* error, which a
// refused statement does not produce. So a query that failed once kept failing
// for the whole TTL: a deadlock, a serialization failure, a statement timeout
// or a permission error pinned itself to the query text that provoked it.
func TestAFailedReplyIsNotStored(t *testing.T) {
	manager := cache.NewManagerWithSize(64)
	m := NewCache(manager, &config.Cache{Enabled: true, TTL: time.Minute},
		protocol.NewPostgresHandler())

	s := cacheSession("SELECT count(*) FROM orders", protocol.StateIdle)

	err := m.Handle(context.Background(), s, func(context.Context, *Session) error {
		// What the gateway does for a backend that answered with an error:
		// bytes were captured and forwarded, and the reply is marked failed.
		s.ResponseCapture.Write(protocol.ErrorResponse(
			protocol.SeverityError, "40P01", "deadlock detected"))
		s.ReplyFailed = true
		return nil
	})
	if err != nil {
		t.Fatalf("Handle returned %v", err)
	}

	if _, hit, _ := manager.Get(CacheKey(s)); hit {
		t.Error("a failed reply was stored; every later client asking the same " +
			"question would be handed that failure for the whole TTL")
	}
}

// The converse, so the guard cannot be satisfied by caching nothing at all.
func TestASuccessfulReplyIsStored(t *testing.T) {
	manager := cache.NewManagerWithSize(64)
	m := NewCache(manager, &config.Cache{Enabled: true, TTL: time.Minute},
		protocol.NewPostgresHandler())

	s := cacheSession("SELECT count(*) FROM orders", protocol.StateIdle)

	err := m.Handle(context.Background(), s, func(context.Context, *Session) error {
		s.ResponseCapture.Write([]byte{'C', 0, 0, 0, 9, 'S', 'E', 'L', 'E', 0})
		s.ReplyFailed = false
		return nil
	})
	if err != nil {
		t.Fatalf("Handle returned %v", err)
	}

	if _, hit, _ := manager.Get(CacheKey(s)); !hit {
		t.Error("a successful reply was not stored, so the cache holds nothing")
	}
}

// A statement inside a transaction may see rows only that transaction can see,
// and one inside an *aborted* transaction is refused with 25P02 — an answer
// about the connection, not the query. Neither may be stored.
func TestInTransactionRepliesAreNotStored(t *testing.T) {
	for name, txState := range map[string]protocol.TransactionState{
		"open transaction":    protocol.StateInTransaction,
		"aborted transaction": protocol.StateError,
	} {
		t.Run(name, func(t *testing.T) {
			manager := cache.NewManagerWithSize(64)
			m := NewCache(manager, &config.Cache{Enabled: true, TTL: time.Minute},
				protocol.NewPostgresHandler())

			s := cacheSession("SELECT count(*) FROM orders", txState)

			called := false
			err := m.Handle(context.Background(), s, func(context.Context, *Session) error {
				called = true
				if s.ResponseCapture != nil {
					s.ResponseCapture.Write([]byte{'C', 0, 0, 0, 9, 'S', 'E', 'L', 'E', 0})
				}
				return nil
			})
			if err != nil {
				t.Fatalf("Handle returned %v", err)
			}
			if !called {
				t.Fatal("the statement never reached the backend")
			}

			if _, hit, _ := manager.Get(CacheKey(s)); hit {
				t.Error("a reply from inside a transaction was stored")
			}
		})
	}
}

// And a stored entry must not be *served* to a statement inside a transaction:
// that transaction may have written rows the cached bytes predate.
func TestInTransactionReadsAreNotServedFromCache(t *testing.T) {
	manager := cache.NewManagerWithSize(64)
	m := NewCache(manager, &config.Cache{Enabled: true, TTL: time.Minute},
		protocol.NewPostgresHandler())

	// Seed an entry from an idle session.
	idle := cacheSession("SELECT count(*) FROM orders", protocol.StateIdle)
	manager.Set(CacheKey(idle), []byte("stale"), time.Minute, nil)

	inTx := cacheSession("SELECT count(*) FROM orders", protocol.StateInTransaction)

	reached := false
	err := m.Handle(context.Background(), inTx, func(context.Context, *Session) error {
		reached = true
		return nil
	})
	if err != nil {
		t.Fatalf("Handle returned %v", err)
	}
	if !reached {
		t.Error("a statement inside a transaction was answered from the cache, " +
			"so it could not see rows its own transaction had written")
	}
}
