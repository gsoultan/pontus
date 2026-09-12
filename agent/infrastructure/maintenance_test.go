package infrastructure

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// drain collects a progress stream's stages and the last message.
func drainStages[T any](ch <-chan T, stage func(T) (string, string)) (stages []string, last string) {
	for msg := range ch {
		s, m := stage(msg)
		stages = append(stages, s)
		if m != "" {
			last = m
		}
	}
	return stages, last
}

func backupStages(ch <-chan *endpoints.BackupProgress) ([]string, string) {
	return drainStages(ch, func(p *endpoints.BackupProgress) (string, string) {
		return p.Stage, p.Message
	})
}

// A backup that reports success having written nothing is worse than an error:
// it is a disaster recovery plan that fails only when it is needed.
func TestBackupRefusesAnUnusableDestination(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"relative", "backup.dump"},
		{"missing directory", "/nonexistent-dir-for-pontus-test/backup.dump"},
		{"a directory", t.TempDir()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateBackupPath(tc.path); err == nil {
				t.Errorf("validateBackupPath(%q) accepted it", tc.path)
			}
		})
	}

	good := filepath.Join(t.TempDir(), "backup.dump")
	if err := validateBackupPath(good); err != nil {
		t.Errorf("a usable path was refused: %v", err)
	}
}

// The refusal has to reach the caller through the stream, because the transport
// does not carry an error raised while the stream is being constructed.
func TestBackupReportsRefusalThroughTheStream(t *testing.T) {
	m := &management{}

	out, err := m.BackupDatabase(context.Background(), &endpoints.BackupDatabaseRequest{
		BackupPath: "relative/path.dump",
		Database:   "app",
	})
	if err != nil {
		t.Fatalf("BackupDatabase returned an error instead of a stream: %v", err)
	}

	stages, last := backupStages(out)
	if len(stages) == 0 || stages[len(stages)-1] != stageError {
		t.Errorf("stages = %v, want the last to be %q", stages, stageError)
	}
	// Never 100: the caller treats that as success.
	for _, s := range stages {
		if s == "Done" {
			t.Error("a refused backup reported Done")
		}
	}
	if last == "" {
		t.Error("the refusal carried no reason")
	}
}

// The whole cluster needs pg_dumpall, which is the only tool that carries roles
// and tablespaces; a single database gets pg_dump's custom format, which is
// compressed and restorable selectively.
func TestDumpToolMatchesTheScope(t *testing.T) {
	if got := dumpToolFor(""); got != "pg_dumpall" {
		t.Errorf("dumpToolFor(cluster) = %q, want pg_dumpall", got)
	}
	if got := dumpToolFor("app"); got != "pg_dump" {
		t.Errorf("dumpToolFor(app) = %q, want pg_dump", got)
	}
}

// The file says which tool restores it, not the caller: a custom-format dump
// handed to psql is executed as SQL and fails on the first byte.
func TestCustomFormatIsDetectedFromTheFile(t *testing.T) {
	dir := t.TempDir()

	custom := filepath.Join(dir, "custom.dump")
	if err := os.WriteFile(custom, []byte("PGDMP\x00\x00rest of archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain.sql")
	if err := os.WriteFile(plain, []byte("-- PostgreSQL database dump\nCREATE TABLE t();\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.sql")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path string
		want bool
	}{{custom, true}, {plain, false}, {empty, false}} {
		got, err := isCustomFormatDump(tc.path)
		if err != nil {
			t.Fatalf("isCustomFormatDump(%s): %v", tc.path, err)
		}
		if got != tc.want {
			t.Errorf("isCustomFormatDump(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
		}
	}

	if _, err := isCustomFormatDump(filepath.Join(dir, "absent")); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestRestoreRefusesAMissingBackup(t *testing.T) {
	m := &management{}

	out, err := m.RestoreDatabase(context.Background(), &endpoints.RestoreDatabaseRequest{
		BackupPath: filepath.Join(t.TempDir(), "absent.dump"),
	})
	if err != nil {
		t.Fatalf("RestoreDatabase returned an error instead of a stream: %v", err)
	}

	stages, last := drainStages(out, func(p *endpoints.RestoreProgress) (string, string) {
		return p.Stage, p.Message
	})
	if len(stages) == 0 || stages[len(stages)-1] != stageError {
		t.Errorf("stages = %v, want the last to be %q", stages, stageError)
	}
	if !strings.Contains(last, "no backup at") {
		t.Errorf("the refusal does not name the cause: %q", last)
	}
}

// Returning early from a promotion that has not finished is the answer that
// costs something, so anything unrecognised waits.
func TestWaitForCompletionDefaultsToWaiting(t *testing.T) {
	for _, value := range []string{"", "true", "yes", "1", "anything"} {
		if !wantsWait(value) {
			t.Errorf("wantsWait(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"false", "no", "0", "off", " FALSE "} {
		if wantsWait(value) {
			t.Errorf("wantsWait(%q) = true, want false", value)
		}
	}
}

// A host running two clusters has at most one of them on 5432, and the socket
// directory differs by distribution. Connecting to the wrong one reports "no
// such file or directory" and looks like the server is down.
func TestConnectionSettingsAreReadNotAssumed(t *testing.T) {
	dir := makeDataDir(t)

	// Nothing configured: PostgreSQL's own default is in force.
	port, socket := connectionSettings(dir)
	if port != defaultPort || socket != "" {
		t.Errorf("defaults = %d/%q, want %d/\"\"", port, socket, defaultPort)
	}

	if err := os.WriteFile(filepath.Join(dir, "postgresql.conf"),
		[]byte("port = 5433 # this cluster\nunix_socket_directories = '/var/run/postgresql, /tmp'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	port, socket = connectionSettings(dir)
	if port != 5433 {
		t.Errorf("port = %d, want 5433", port)
	}
	// The first entry is the one to dial.
	if socket != "/var/run/postgresql" {
		t.Errorf("socket dir = %q, want /var/run/postgresql", socket)
	}

	// auto.conf wins at load time, so it holds the value in force.
	if err := os.WriteFile(filepath.Join(dir, "postgresql.auto.conf"),
		[]byte("port = 5555\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if port, _ = connectionSettings(dir); port != 5555 {
		t.Errorf("port = %d, want auto.conf's 5555", port)
	}
}

func TestConnArgsPointAtTheCluster(t *testing.T) {
	h := &pgHost{port: 5433, socketDir: "/var/run/postgresql"}
	args := strings.Join(h.connArgs(), " ")
	if !strings.Contains(args, "--port=5433") {
		t.Errorf("connArgs = %q, want the port", args)
	}
	if !strings.Contains(args, "--host=/var/run/postgresql") {
		t.Errorf("connArgs = %q, want the socket directory", args)
	}

	// With no socket directory configured the tool uses its own default rather
	// than being handed an empty --host, which would mean TCP to localhost.
	bare := &pgHost{port: 5432}
	if strings.Contains(strings.Join(bare.connArgs(), " "), "--host") {
		t.Errorf("connArgs = %v, want no --host when none is configured", bare.connArgs())
	}
}

// A missing binary is a different problem from an operation that ran and
// failed, and reaches an operator as a failed backup with no hint otherwise.
func TestRequireToolNamesAMissingBinary(t *testing.T) {
	if err := requireTool("definitely-not-a-real-postgres-tool"); err == nil {
		t.Error("a missing tool was accepted")
	} else if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error does not say what is wrong: %v", err)
	}
	if err := requireTool("go"); err != nil {
		t.Errorf("an installed tool was rejected: %v", err)
	}
}
