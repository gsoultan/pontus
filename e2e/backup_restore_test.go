//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
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

// Creating a cluster and destroying it, through a real agent.
//
// Both replaced stubs. RemoveDatabase is the one that matters most: it reported
// that it had deleted a database's storage without touching anything, which is
// the failure direction that makes an operator believe data is gone when it is
// not.
func TestInitializeAndRemoveACluster(t *testing.T) {
	c := startLocalCluster(t)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	agent, closeAgent := agentClientFor(t, c.primary.agentAddr())
	defer closeAgent()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", localAgentToken)

	// A directory alongside the running pair, so nothing under test is at risk.
	fresh := filepath.Join(filepath.Dir(c.primary.dataDir), "initialized")

	init, err := agent.InitializeDatabase(ctx, &endpoints.InitializeDatabaseRequest{
		DataDirectory: fresh,
		InitialUser:   currentUser(t),
	})
	if err != nil {
		t.Fatalf("InitializeDatabase: %v", err)
	}
	stage, message := lastOf(init.Recv, func(p *endpoints.InitializeProgress) (string, string) {
		return p.Stage, p.Message
	})
	if stage != "Done" {
		t.Fatalf("initialize ended at stage %q: %s", stage, message)
	}

	// A cluster is a directory with a PG_VERSION in it. Anything less is a
	// progress bar.
	if _, err := os.Stat(filepath.Join(fresh, "PG_VERSION")); err != nil {
		t.Fatalf("initialize reported success and created no cluster: %v", err)
	}

	// Initialising again over the same directory has to be refused.
	again, err := agent.InitializeDatabase(ctx, &endpoints.InitializeDatabaseRequest{
		DataDirectory: fresh,
		InitialUser:   currentUser(t),
	})
	if err != nil {
		t.Fatalf("InitializeDatabase: %v", err)
	}
	stage, message = lastOf(again.Recv, func(p *endpoints.InitializeProgress) (string, string) {
		return p.Stage, p.Message
	})
	if stage == "Done" {
		t.Error("initialising over an existing cluster was allowed")
	}
	if !strings.Contains(message, "already holds") {
		t.Errorf("the refusal does not name the cause: %q", message)
	}

	// And removal really removes.
	resp, err := agent.RemoveDatabase(ctx, &endpoints.RemoveDatabaseRequest{
		DataDirectory: fresh,
		DeleteData:    true,
	})
	if err != nil {
		t.Fatalf("RemoveDatabase: %v", err)
	}
	if !resp.Success {
		t.Fatalf("removal failed: %s", resp.ErrorMessage)
	}
	if _, err := os.Stat(filepath.Join(fresh, "PG_VERSION")); err == nil {
		t.Error("removal reported success and the cluster is still there")
	}

	// The pair under test is untouched.
	if _, err := queryInt(c.primary.dsn(), "SELECT 1"); err != nil {
		t.Errorf("the primary stopped answering: %v", err)
	}
}

// currentUser is the account these tests run as, which is the only one that can
// own a cluster here — the agent is not root, so it cannot become another.
func currentUser(t *testing.T) string {
	t.Helper()

	u, err := user.Current()
	if err != nil {
		t.Fatalf("looking up the current user: %v", err)
	}
	return u.Username
}
