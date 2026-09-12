package infrastructure

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Initialising over a live cluster is the mistake this has to refuse. initdb
// would refuse too, but only after the agent has created and chowned the
// directory — and the message an operator gets should name the cluster rather
// than be a tool error to interpret.
func TestInitRefusesToOverwriteAnExistingCluster(t *testing.T) {
	dir := makeDataDir(t)
	m := &management{}

	_, err := m.planInit(&endpoints.InitializeDatabaseRequest{DataDirectory: dir})
	if err == nil {
		t.Fatal("initialising over an existing cluster was accepted")
	}
	if !strings.Contains(err.Error(), "already holds a PostgreSQL cluster") {
		t.Errorf("error does not name the cause: %v", err)
	}
}

func TestInitRefusesAnUnusableRequest(t *testing.T) {
	m := &management{}

	// No directory anywhere: not in the request, not on the agent.
	if _, err := m.planInit(&endpoints.InitializeDatabaseRequest{}); err == nil {
		t.Error("a request with no data directory was accepted")
	}
	if _, err := m.planInit(&endpoints.InitializeDatabaseRequest{
		DataDirectory: "relative/path"}); err == nil {
		t.Error("a relative data directory was accepted")
	}

	// An empty directory that does not exist yet is the ordinary case.
	fresh := filepath.Join(t.TempDir(), "new-cluster")
	plan, err := m.planInit(&endpoints.InitializeDatabaseRequest{DataDirectory: fresh})
	if err != nil {
		t.Fatalf("a fresh directory was refused: %v", err)
	}
	if plan.owner != DefaultSuperuser {
		t.Errorf("owner = %q, want the default %q", plan.owner, DefaultSuperuser)
	}
}

// The agent's own -data-dir is the fallback when the request names none.
func TestInitFallsBackToTheAgentsCluster(t *testing.T) {
	fresh := filepath.Join(t.TempDir(), "new-cluster")
	m := &management{dataDir: fresh, dbUser: "someone"}

	plan, err := m.planInit(&endpoints.InitializeDatabaseRequest{})
	if err != nil {
		t.Fatalf("planInit: %v", err)
	}
	if plan.dataDir != fresh {
		t.Errorf("dataDir = %q, want %q", plan.dataDir, fresh)
	}
	if plan.owner != "someone" {
		t.Errorf("owner = %q, want the agent's configured user", plan.owner)
	}

	// The request wins over the agent's default when it names one.
	other := filepath.Join(t.TempDir(), "other")
	plan, err = m.planInit(&endpoints.InitializeDatabaseRequest{
		DataDirectory: other, InitialUser: "requested"})
	if err != nil {
		t.Fatalf("planInit: %v", err)
	}
	if plan.dataDir != other || plan.owner != "requested" {
		t.Errorf("plan = %q/%q, want %q/requested", plan.dataDir, plan.owner, other)
	}
}

// A host can carry several major versions, and taking whatever is first on PATH
// initialises a cluster one version's server then refuses to start.
func TestVersionedBinDirIsChecked(t *testing.T) {
	if dir, err := versionedBinDir(""); err != nil || dir != "" {
		t.Errorf("no version should mean PATH: got %q, %v", dir, err)
	}
	if _, err := versionedBinDir("not-a-version"); err == nil {
		t.Error("a non-numeric version was accepted")
	}
	// A major that is not installed is named rather than silently falling back.
	if _, err := versionedBinDir("999"); err == nil {
		t.Error("an uninstalled major version was accepted")
	}
}

// A password on a command line is readable by every process on the host.
func TestPasswordGoesToAFileNotAnArgument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pw")

	if err := writePasswordFile(path, "s3cret", nil); err != nil {
		t.Fatalf("writePasswordFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the password file was not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("password file mode = %o, want 600", perm)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "s3cret" {
		t.Errorf("password file holds %q", content)
	}
}

// Deleting is the point of the request, so a reply that says "success" without
// having deleted must say so.
func TestRemoveWithoutDeleteDataSaysTheDataIsKept(t *testing.T) {
	dir := makeDataDir(t)
	m := &management{dataDir: dir}

	resp, err := m.RemoveDatabase(context.Background(), &endpoints.RemoveDatabaseRequest{
		DataDirectory: dir,
		DeleteData:    false,
	})
	if err != nil {
		t.Fatalf("RemoveDatabase: %v", err)
	}
	if !resp.Success {
		t.Fatalf("stopping a stopped cluster failed: %s", resp.ErrorMessage)
	}
	if !strings.Contains(resp.ErrorMessage, "data kept") {
		t.Errorf("the reply does not say the data was kept: %q", resp.ErrorMessage)
	}
	if _, err := os.Stat(filepath.Join(dir, "PG_VERSION")); err != nil {
		t.Error("the cluster was deleted despite delete_data being unset")
	}
}

func TestRemoveDeletesTheContentsAndKeepsTheDirectory(t *testing.T) {
	dir := makeDataDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "base", "1"), 0o700); err != nil {
		t.Fatal(err)
	}
	m := &management{dataDir: dir}

	resp, err := m.RemoveDatabase(context.Background(), &endpoints.RemoveDatabaseRequest{
		DataDirectory: dir,
		DeleteData:    true,
	})
	if err != nil {
		t.Fatalf("RemoveDatabase: %v", err)
	}
	if !resp.Success {
		t.Fatalf("removal failed: %s", resp.ErrorMessage)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the directory itself was removed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the cluster still holds %d entries", len(entries))
	}
}

// The same guard as every other destructive path: a directory that is not a
// cluster is refused rather than emptied.
func TestRemoveRefusesSomethingThatIsNotACluster(t *testing.T) {
	notACluster := t.TempDir()
	if err := os.WriteFile(filepath.Join(notACluster, "important.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &management{}

	resp, err := m.RemoveDatabase(context.Background(), &endpoints.RemoveDatabaseRequest{
		DataDirectory: notACluster,
		DeleteData:    true,
	})
	if err != nil {
		t.Fatalf("RemoveDatabase: %v", err)
	}
	if resp.Success {
		t.Fatal("a directory with no PG_VERSION was accepted for deletion")
	}
	if _, err := os.Stat(filepath.Join(notACluster, "important.txt")); err != nil {
		t.Error("the refusal still deleted the contents")
	}
}

// A fabricated task id is worse than a refusal: it is visible the day the job
// was supposed to fire rather than the day it was set up.
func TestScheduleMaintenanceRefusesRatherThanInventingATask(t *testing.T) {
	m := &management{}

	resp, err := m.ScheduleMaintenance(context.Background(), &endpoints.ScheduleMaintenanceRequest{
		TaskType: "vacuum", Database: "app", CronExpression: "0 3 * * *",
	})
	if err != nil {
		t.Fatalf("ScheduleMaintenance: %v", err)
	}
	if resp.Success {
		t.Error("an unimplemented schedule reported success")
	}
	if resp.TaskId != "" {
		t.Errorf("a task id was invented: %q", resp.TaskId)
	}
	if !strings.Contains(resp.ErrorMessage, "not implemented") {
		t.Errorf("the reply does not say it is unimplemented: %q", resp.ErrorMessage)
	}
}
