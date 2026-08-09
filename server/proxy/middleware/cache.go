package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/gsoultan/pontus/pkg/buffer"
	"github.com/gsoultan/pontus/pkg/config"
	"github.com/gsoultan/pontus/pkg/observability"
	"github.com/gsoultan/pontus/server/internal/cache"
	"github.com/gsoultan/pontus/server/internal/pool"
	"github.com/gsoultan/pontus/server/internal/protocol"
)

type Cache struct {
	manager *cache.Manager
	config  *config.Cache
	handler protocol.Handler
}

func NewCache(manager *cache.Manager, config *config.Cache, handler protocol.Handler) *Cache {
	return &Cache{
		manager: manager,
		config:  config,
		handler: handler,
	}
}

func (m *Cache) Handle(ctx context.Context, s *Session, next HandlerFunc) error {
	if m.manager == nil || m.config == nil || !m.config.Enabled || s.Normalized == "" {
		return next(ctx, s)
	}

	// Only a whole request/response exchange may be cached. An extended-protocol
	// Parse carries a SELECT, so it normalises and classifies as a cacheable
	// read — and answering it with a stored result set instead of ParseComplete
	// desynchronises the connection. The client then Binds a statement the
	// server never parsed and gets SQLSTATE 26000, which is what every
	// prepared-statement client saw from its second connection onward.
	if !m.handler.Cacheable(s.Data) {
		return next(ctx, s)
	}

	// A write is never served from cache, and it evicts what it invalidates.
	if !s.QueryInfo.ReadOnly {
		err := next(ctx, s)
		if err == nil && len(s.QueryInfo.AffectedTables) > 0 {
			m.manager.Invalidate(s.QueryInfo.AffectedTables)
		}
		return err
	}

	if s.QueryInfo.InTransaction {
		return next(ctx, s)
	}

	key := CacheKey(s)

	cachedResponse, hit, needsRevalidate := m.manager.Get(key)
	if hit {
		if _, err := s.Client.Write(cachedResponse); err != nil {
			// A failed write to the client is the end of this session, not
			// something to discard: the caller closes the connection on error.
			return err
		}
		observability.QueriesTotal.WithLabelValues("cache", "read", "hit").Inc()

		if needsRevalidate {
			m.revalidate(key, s)
		}
		return nil
	}

	// Cache miss: prepare to capture response
	s.ResponseCapture = new(bytes.Buffer)
	err := next(ctx, s)
	if err == nil && s.ResponseCapture.Len() > 0 {
		m.manager.Set(key, s.ResponseCapture.Bytes(), m.config.TTL, s.QueryInfo.AffectedTables)
		observability.QueriesTotal.WithLabelValues("cache", "write", "miss").Inc()
	}

	return err
}

// revalidate refreshes a stale entry in the background.
//
// The backend and the request bytes are captured here, not read from the session
// inside the goroutine. The gateway clears Session.Backend once a transaction
// goes idle and reuses Session.Data — it is a view into the one buffer the read
// loop overwrites on the next client message — so a goroutine that reads either
// of them later gets whatever the session moved on to.
func (m *Cache) revalidate(key string, s *Session) {
	backend := s.Backend
	if backend == nil {
		// Nothing to refresh against. The entry stays stale until it expires,
		// which is the whole point of a stale window.
		return
	}

	data := bytes.Clone(s.Data)
	tables := slices.Clone(s.QueryInfo.AffectedTables)
	state := s.State

	go m.backgroundRefresh(key, data, state, backend, tables)
}

func (m *Cache) backgroundRefresh(key string, data []byte, state *protocol.SessionState, backend pool.Backend, tables []string) {
	slog.Debug("Stale cache hit, triggering revalidation")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := backend.Acquire(ctx)
	if err != nil {
		slog.Warn("Background refresh failed to acquire connection", "error", err)
		return
	}
	defer backend.Release(conn)

	// Replay state
	if err := m.handler.ReplaySessionState(ctx, conn, state); err != nil {
		return
	}

	if _, err := conn.Write(data); err != nil {
		return
	}

	// Capture response
	buf := buffer.Get()
	defer buffer.Put(buf)
	capture := new(bytes.Buffer)

	for {
		n, err := conn.Read(buf)
		if n > 0 {
			capture.Write(buf[:n])
			state, _ := m.handler.PeekTransactionState(buf[:n])
			if state != protocol.StatePartial {
				break
			}
		}
		if err != nil {
			return
		}
	}

	if capture.Len() > 0 {
		// Carry the table associations across. Re-storing with nil tables left an
		// entry that no write could ever invalidate — permanently stale.
		m.manager.Set(key, capture.Bytes(), m.config.TTL, tables)
		slog.Debug("Background refresh completed")
	}
}
