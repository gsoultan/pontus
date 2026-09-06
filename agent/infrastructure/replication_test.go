package infrastructure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// makeDataDir builds something that looks enough like a PostgreSQL cluster to
// pass validation.
func makeDataDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "pgdata")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the data directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("17\n"), 0o600); err != nil {
		t.Fatalf("writing PG_VERSION: %v", err)
	}
	return dir
}

// The rebuild empties this path. A mistyped or undetected directory would be
// deleted just as willingly as the right one, so the check is not "does it
// exist" but "does it hold the file only a data directory has".
func TestValidateDataDirRefusesAnythingThatIsNotACluster(t *testing.T) {
	real := makeDataDir(t)
	if err := validateDataDir(real); err != nil {
		t.Errorf("a real data directory was refused: %v", err)
	}

	// Present, but not a cluster: this is the case that would otherwise be
	// erased.
	notACluster := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(notACluster, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateDataDir(notACluster); err == nil {
		t.Error("a directory with no PG_VERSION was accepted as a data directory")
	}

	for _, dir := range []string{"", "   ", "relative/path", "/", "/var"} {
		if err := validateDataDir(dir); err == nil {
			t.Errorf("validateDataDir(%q) accepted it", dir)
		}
	}
}

// A rebuild has to be refused before anything is touched, not partway through.
func TestPlanReplicationRefusesBadRequestsBeforeTouchingAnything(t *testing.T) {
	m := &management{}
	dir := makeDataDir(t)

	for _, tc := range []struct {
		name string
		req  *endpoints.SetupReplicationRequest
	}{
		{"no primary", &endpoints.SetupReplicationRequest{PrimaryPort: 5432, DataDirectory: dir}},
		{"no port", &endpoints.SetupReplicationRequest{PrimaryHost: "10.0.0.1", DataDirectory: dir}},
		{"port out of range", &endpoints.SetupReplicationRequest{PrimaryHost: "10.0.0.1", PrimaryPort: 70000, DataDirectory: dir}},
		{"data directory is not a cluster", &endpoints.SetupReplicationRequest{
			PrimaryHost: "10.0.0.1", PrimaryPort: 5432, DataDirectory: t.TempDir()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.planReplication(tc.req); err == nil {
				t.Error("the request was accepted")
			}
		})
	}

	setup, err := m.planReplication(&endpoints.SetupReplicationRequest{
		PrimaryHost:     "10.0.0.1",
		PrimaryPort:     6432,
		DataDirectory:   dir,
		ReplicationUser: "repl",
		SlotName:        "pontus",
	})
	if err != nil {
		t.Fatalf("a valid request was refused: %v", err)
	}
	if setup.primaryAddr() != "10.0.0.1:6432" {
		t.Errorf("primaryAddr = %q, want 10.0.0.1:6432", setup.primaryAddr())
	}
}

// A cluster rewound without these starts as a primary on the timeline it just
// abandoned, which is the split brain the rebuild was supposed to end.
func TestWriteStandbyConfigMakesTheNodeAStandby(t *testing.T) {
	dir := makeDataDir(t)
	s := &replicationSetup{
		dataDir: dir, primary: "10.0.0.1", port: 6432,
		user: "repl", password: "s3cret", slot: "pontus",
	}

	if err := s.writeStandbyConfig(); err != nil {
		t.Fatalf("writeStandbyConfig: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "standby.signal")); err != nil {
		t.Errorf("standby.signal was not written: %v", err)
	}

	conf, err := os.ReadFile(filepath.Join(dir, "postgresql.auto.conf"))
	if err != nil {
		t.Fatalf("reading postgresql.auto.conf: %v", err)
	}
	for _, want := range []string{"host=10.0.0.1", "port=6432", "user=repl", "primary_slot_name = 'pontus'"} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("postgresql.auto.conf is missing %q:\n%s", want, conf)
		}
	}
}

// Running it twice must not leave two primary_conninfo lines whose last-wins
// order is an accident — a rebuild is retried, so this is the ordinary case.
func TestWriteStandbyConfigReplacesRatherThanAppends(t *testing.T) {
	dir := makeDataDir(t)
	autoConf := filepath.Join(dir, "postgresql.auto.conf")
	if err := os.WriteFile(autoConf,
		[]byte("# managed\nprimary_conninfo = 'host=old port=1'\nwork_mem = '32MB'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &replicationSetup{dataDir: dir, primary: "10.0.0.2", port: 7432}
	if err := s.writeStandbyConfig(); err != nil {
		t.Fatalf("writeStandbyConfig: %v", err)
	}
	if err := s.writeStandbyConfig(); err != nil {
		t.Fatalf("writeStandbyConfig again: %v", err)
	}

	conf, err := os.ReadFile(autoConf)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(conf), "primary_conninfo"); n != 1 {
		t.Errorf("postgresql.auto.conf has %d primary_conninfo lines, want 1:\n%s", n, conf)
	}
	if strings.Contains(string(conf), "host=old") {
		t.Errorf("the previous primary was left in place:\n%s", conf)
	}
	// Settings this rebuild does not own are left alone.
	if !strings.Contains(string(conf), "work_mem = '32MB'") {
		t.Errorf("an unrelated setting was dropped:\n%s", conf)
	}
}

// Copy first, destroy last. The obvious order leaves a window minutes long
// where the node holds nothing and only the agent knows how to refill it — and
// the agent dies with the database whenever they share a container.
func TestSwapInStagingKeepsTheOldClusterUntilItIsReplaced(t *testing.T) {
	dir := makeDataDir(t)
	s := &replicationSetup{dataDir: dir, primary: "h", port: 5432}

	// A staged copy, as pg_basebackup would have left it.
	if err := os.MkdirAll(s.stagingDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.stagingDir(), "PG_VERSION"), []byte("17\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.stagingDir(), "NEW"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "OLD"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.swapInStaging(); err != nil {
		t.Fatalf("swapInStaging: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "NEW")); err != nil {
		t.Errorf("the new copy is not in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "OLD")); err == nil {
		t.Error("the old cluster is still in the data directory")
	}
	// Kept until the replacement is proven, so a failed start has something to
	// go back to.
	if _, err := os.Stat(filepath.Join(s.previousDir(), "OLD")); err != nil {
		t.Errorf("the superseded cluster was discarded before the new one started: %v", err)
	}

	s.discardPrevious()
	if _, err := os.Stat(s.previousDir()); err == nil {
		t.Error("the superseded cluster was not cleaned up after a successful start")
	}
}

// An interrupted rebuild leaves a staging directory behind, and the next
// attempt must not inherit it — pg_basebackup refuses a target that is not
// empty, so a stale one would fail every retry.
func TestClearStagingRemovesWhatAnInterruptedRebuildLeft(t *testing.T) {
	dir := makeDataDir(t)
	s := &replicationSetup{dataDir: dir, primary: "h", port: 5432}

	for _, d := range []string{s.stagingDir(), s.previousDir()} {
		if err := os.MkdirAll(filepath.Join(d, "base"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.clearStaging(); err != nil {
		t.Fatalf("clearStaging: %v", err)
	}
	for _, d := range []string{s.stagingDir(), s.previousDir()} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s survived clearStaging", d)
		}
	}

	// The real data directory is not staging and must be untouched.
	if _, err := os.Stat(filepath.Join(dir, "PG_VERSION")); err != nil {
		t.Errorf("clearStaging touched the data directory: %v", err)
	}
}

// A password with a quote in it must not break out of the conninfo string.
func TestConninfoQuotesAreEscaped(t *testing.T) {
	dir := makeDataDir(t)
	s := &replicationSetup{dataDir: dir, primary: "h", port: 5432, user: "u", password: "pa'ss"}

	if err := s.writeStandbyConfig(); err != nil {
		t.Fatalf("writeStandbyConfig: %v", err)
	}
	conf, err := os.ReadFile(filepath.Join(dir, "postgresql.auto.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), "pa''ss") {
		t.Errorf("the quote was not doubled, so the value is unterminated:\n%s", conf)
	}
}

func TestStripSettingLeavesOthersAlone(t *testing.T) {
	in := "shared_buffers = '1GB'\nprimary_conninfo = 'x'\nwork_mem = '4MB'\n"
	out := stripSetting(in, "primary_conninfo")

	if strings.Contains(out, "primary_conninfo") {
		t.Errorf("the setting was not removed: %q", out)
	}
	for _, keep := range []string{"shared_buffers", "work_mem"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%s was removed as collateral: %q", keep, out)
		}
	}
}

// Stopping the database is a step in the middle of the sequence. Where the
// database is PID 1 — every database-in-a-container deployment — stopping it
// takes the agent down with it, so the rebuild ends there every time and the
// caller retries into the same wall.
func TestIsPostgresProcessRecognisesTheDatabase(t *testing.T) {
	for _, name := range []string{"postgres", "postmaster", " Postgres ", "POSTGRES"} {
		if !isPostgresProcess(name) {
			t.Errorf("isPostgresProcess(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"systemd", "init", "sh", "pontus-agent", ""} {
		if isPostgresProcess(name) {
			t.Errorf("isPostgresProcess(%q) = true, want false", name)
		}
	}
}
