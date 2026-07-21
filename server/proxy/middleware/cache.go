package middleware

import (
	"bytes"
	"context"
	"log/slog"
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
	if m.manager == nil || m.config == nil || !m.config.Enabled || !s.QueryInfo.ReadOnly || s.QueryInfo.InTransaction || s.Normalized == "" {
		return next(ctx, s)
	}

	cachedResponse, hit, needsRevalidate := m.manager.Get(s.Normalized)
	if hit {
		s.Client.Write(cachedResponse)
		observability.QueriesTotal.WithLabelValues("cache", "read", "hit").Inc()

		if needsRevalidate {
			// Revalidate in background (SWR)
			go m.backgroundRefresh(s.Normalized, s.Data, s.State, s.Backend)
		}
		return nil // Request served from cache
	}

	// Cache miss: prepare to capture response
	s.ResponseCapture = new(bytes.Buffer)
	err := next(ctx, s)
	if err == nil && s.ResponseCapture.Len() > 0 {
		m.manager.Set(s.Normalized, s.ResponseCapture.Bytes(), m.config.TTL, s.QueryInfo.AffectedTables)
		observability.QueriesTotal.WithLabelValues("cache", "write", "miss").Inc()
	}

	return err
}

func (m *Cache) backgroundRefresh(key string, data []byte, state *protocol.SessionState, backend pool.Backend) {
	slog.Debug("Stale cache hit, triggering revalidation", "query", key)

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
		m.manager.Set(key, capture.Bytes(), m.config.TTL, nil)
		slog.Debug("Background refresh completed", "query", key)
	}
}
