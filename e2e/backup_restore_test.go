//go:build e2e

package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// Backup and restore, run against a real database through a real agent.
//
// These were stubs that emitted a progress bar and reported success without
// running anything: an operator who clicked "Backup" got a green tick and no
// backup, which is worse than an error — it is a disaster recovery plan that
// fails only when it is needed. A round trip is the only test that catches
// that, because every weaker one passes against the stub.
func TestBackupAndRestoreRoundTrip(t *testing.T) {
	c := startLocalCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Something to lose.
	conn := directConn(t, ctx, c.primary.dsn())
	if _, err := conn.Exec(ctx,
		"CREATE TABLE keepsake (id int primary key, note text)"); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if _, err := conn.Exec(ctx,
		"INSERT INTO keepsake VALUES (1, 'survives a restore')"); err != nil {
		t.Fatalf("seeding the table: %v", err)
	}
	conn.Close(context.Background())

	agent, closeAgent := agentClientFor(t, c.primary.agentAddr())
	defer closeAgent()

	// The token rides on every call, as it does in production.
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", localAgentToken)

	backupPath := filepath.Join(t.TempDir(), "keepsake.dump")

	// A backup that reports success must have written a file.
	stream, err := agent.BackupDatabase(ctx, &endpoints.BackupDatabaseRequest{
		BackupPath: backupPath,
		Database:   "postgres",
	})
	if err != nil {
		t.Fatalf("BackupDatabase: %v", err)
	}
	stage, message := lastOf(stream.Recv, func(p *endpoints.BackupProgress) (string, string) {
		return p.Stage, p.Message
	})
	if stage != "Done" {
		t.Fatalf("backup ended at stage %q: %s", stage, message)
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("the backup reported success and wrote nothing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("the backup file is empty")
	}
	t.Logf("backup wrote %d bytes", info.Size())

	// Lose it.
	conn = directConn(t, ctx, c.primary.dsn())
	if _, err := conn.Exec(ctx, "DROP TABLE keepsake"); err != nil {
		t.Fatalf("dropping the table: %v", err)
	}
	conn.Close(context.Background())

	// Get it back.
	restore, err := agent.RestoreDatabase(ctx, &endpoints.RestoreDatabaseRequest{
		BackupPath:     backupPath,
		TargetDatabase: "postgres",
	})
	if err != nil {
		t.Fatalf("RestoreDatabase: %v", err)
	}
	stage, message = lastOf(restore.Recv, func(p *endpoints.RestoreProgress) (string, string) {
		return p.Stage, p.Message
	})
	if stage != "Done" {
		t.Fatalf("restore ended at stage %q: %s", stage, message)
	}

	note, err := queryString(c.primary.dsn(), "SELECT note FROM keepsake WHERE id = 1")
	if err != nil {
		t.Fatalf("the restored table is not readable: %v", err)
	}
	if note != "survives a restore" {
		t.Errorf("restored note = %q, want %q", note, "survives a restore")
	}
}

// progress is the shape every agent progress stream shares.
type progress interface{ GetStage() string }

// lastOf drains a progress stream and returns where it ended.
//
// The final stage is the whole assertion: a stub reported "Done" at 100% having
// run nothing, so anything short of reading the end of the stream — and then
// checking the world — passes against it.
// A vacuum that reports success must have run. ANALYZE is the observable half:
// it sets pg_stat_user_tables.last_analyze, which nothing else here touches.
func TestVacuumActuallyRuns(t *testing.T) {
	c := startLocalCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	conn := directConn(t, ctx, c.primary.dsn())
	if _, err := conn.Exec(ctx, "CREATE TABLE vacuumed (id int)"); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO vacuumed SELECT generate_series(1, 100)"); err != nil {
		t.Fatalf("seeding the table: %v", err)
	}
	conn.Close(context.Background())

	analyzed := func() int {
		n, err := queryInt(c.primary.dsn(),
			"SELECT count(*) FROM pg_stat_user_tables WHERE relname = 'vacuumed' AND last_analyze IS NOT NULL")
		if err != nil {
			return -1
		}
		return n
	}
	if analyzed() != 0 {
		t.Fatal("the table was already analyzed before the test ran")
	}

	agent, closeAgent := agentClientFor(t, c.primary.agentAddr())
	defer closeAgent()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", localAgentToken)

	stream, err := agent.VacuumDatabase(ctx, &endpoints.VacuumDatabaseRequest{
		Database: "postgres",
		Analyze:  true,
	})
	if err != nil {
		t.Fatalf("VacuumDatabase: %v", err)
	}
	stage, message := lastOf(stream.Recv, func(p *endpoints.VacuumProgress) (string, string) {
		return p.Stage, p.Message
	})
	if stage != "Done" {
		t.Fatalf("vacuum ended at stage %q: %s", stage, message)
	}

	// The statistics collector is asynchronous, so the write may land a moment
	// after the command returns.
	if !waitFor(30*time.Second, func() bool { return analyzed() == 1 }) {
		t.Error("the vacuum reported success but the table was never analyzed")
	}
}

func lastOf[T any](recv func() (T, error), stage func(T) (string, string)) (string, string) {
	var lastStage, lastMessage string
	for {
		msg, err := recv()
		if err != nil {
			return lastStage, lastMessage
		}
		s, m := stage(msg)
		lastStage = s
		if m != "" {
			lastMessage = m
		}
	}
}

// agentClientFor dials an agent directly.
//
// The proxy's own client lives under server/internal, which e2e cannot import,
// so this is the generated client plus the one piece of wiring that matters:
// the token, which guards root-level operations.
func agentClientFor(t *testing.T, addr string) (service.AgentServiceClient, func()) {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialling the agent at %s: %v", addr, err)
	}
	return service.NewAgentServiceClient(conn), func() { conn.Close() }
}
