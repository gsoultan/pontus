package pool

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pure-Go driver; CGO stays disabled
)

// AdminSession is Pontus's own authenticated connection to a backend.
//
// It exists because the proxy has no session of its own on the pooled path: a
// client's startup packet is forwarded rather than replaced, so a pooled
// connection is a raw socket until some client has handshaked it. Everything
// the control plane needs to ask a database — is it in recovery, how far behind
// is it, which slots exist — was previously run on whichever pooled connection
// happened to be lying around, which meant it ran as whatever user last used
// it, or failed outright against a database that was plainly up.
//
// Deliberately separate from the client pool. Authenticating the pooled
// connections would break the transparency the proxy depends on; this is a
// second, tiny channel used only by Pontus.
type AdminSession struct {
	db *sql.DB

	mu      sync.RWMutex
	lastErr error
}

// adminMaxConns caps the admin channel. Administrative queries are periodic and
// serial, so one connection is enough — and one is what an operator sizing
// max_connections should have to account for.
const adminMaxConns = 1

// NewAdminSession opens the administrative channel for a backend.
//
// The DSN is the operator's: Pontus cannot derive credentials from a client,
// and guessing them would be worse than not having them. An empty DSN yields a
// nil session, which every caller treats as "not configured" rather than as an
// error — an operator who has not set one keeps the previous behaviour.
func NewAdminSession(dsn string, connectTimeout time.Duration) (*AdminSession, error) {
	if dsn == "" {
		return nil, nil
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open admin session: %w", err)
	}

	db.SetMaxOpenConns(adminMaxConns)
	db.SetMaxIdleConns(adminMaxConns)
	// Recycled hourly so a long-lived process does not hold one connection
	// against the database for its entire lifetime.
	db.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	// Verified eagerly: a bad DSN should be reported at startup, not discovered
	// during a failover.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("admin session unusable: %w", err)
	}

	return &AdminSession{db: db}, nil
}

// Available reports whether the administrative channel can be used.
func (a *AdminSession) Available() bool {
	return a != nil && a.db != nil
}

// QueryRow runs a query returning a single row. Parameters are bound by the
// driver, so identifiers are the only thing callers must still validate.
func (a *AdminSession) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return a.db.QueryRowContext(ctx, query, args...)
}

// Query runs a query returning many rows.
func (a *AdminSession) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	a.record(err)
	return rows, err
}

// Exec runs a statement.
func (a *AdminSession) Exec(ctx context.Context, query string, args ...any) error {
	_, err := a.db.ExecContext(ctx, query, args...)
	a.record(err)
	return err
}

// Ping verifies the channel is usable.
func (a *AdminSession) Ping(ctx context.Context) error {
	err := a.db.PingContext(ctx)
	a.record(err)
	return err
}

// LastError returns the most recent failure, for surfacing why administrative
// features are unavailable rather than leaving them silently empty.
func (a *AdminSession) LastError() error {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastErr
}

func (a *AdminSession) record(err error) {
	a.mu.Lock()
	a.lastErr = err
	a.mu.Unlock()
}

// Close releases the channel.
func (a *AdminSession) Close() error {
	if !a.Available() {
		return nil
	}
	return a.db.Close()
}
