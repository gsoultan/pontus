//go:build e2e

package pool

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func liveDSN() string {
	addr := os.Getenv("PONTUS_E2E_BACKEND")
	if addr == "" {
		addr = "127.0.0.1:5432"
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		envOr("PONTUS_E2E_USER", "postgres"),
		envOr("PONTUS_E2E_PASSWORD", "postgres"),
		addr,
		envOr("PONTUS_E2E_DB", "postgres"))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Every deep check runs this statement. A syntax error, a function missing on
// an older server, or a permission problem reading pg_stat_wal_receiver would
// fail the backend outright — a worse outcome than the unmeasured lag it was
// added to fix. Unit tests cover the decision logic; only a real server can
// tell us the SQL is accepted.
func TestReplicationStatusQueryRunsOnRealPostgres(t *testing.T) {
	addr := os.Getenv("PONTUS_E2E_BACKEND")
	if addr == "" {
		addr = "127.0.0.1:5432"
	}
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Skipf("no PostgreSQL at %s: %v", addr, err)
	}
	conn.Close()

	db, err := sql.Open("pgx", liveDSN())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var (
		status    replicationStatus
		replaySec float64
	)
	if err := db.QueryRowContext(ctx, replicationStatusQuery).
		Scan(&status.inRecovery, &replaySec, &status.caughtUp, &status.streaming); err != nil {
		t.Fatalf("the deep-check replication query was rejected by a real backend: %v", err)
	}
	status.replayAge = time.Duration(replaySec * float64(time.Second))

	t.Logf("in_recovery=%v replay_age=%v caught_up=%v streaming=%v -> lag=%v",
		status.inRecovery, status.replayAge.Round(time.Millisecond),
		status.caughtUp, status.streaming, status.lag())

	// A standalone primary must report no lag. Getting this wrong would apply
	// the balancer's staleness penalty to the write node.
	if !status.inRecovery && status.lag() != 0 {
		t.Errorf("a primary reported lag %v", status.lag())
	}
}
