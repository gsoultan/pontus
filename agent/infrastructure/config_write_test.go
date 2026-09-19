package infrastructure

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/agent/infrastructure/validator"
	"github.com/gsoultan/pontus/agent/services"
	"github.com/gsoultan/pontus/api/proto/endpoints"
)

const goodHBA = "local all all peer\nhost all all 127.0.0.1/32 scram-sha-256\n"

// The finding this file exists for: UpdateConfig validated the content and then
// returned Success: true over a commented-out os.WriteFile. The dashboard,
// pontusctl and the ConnectRPC API all reported the edit had been applied.
func TestUpdateConfigActuallyWritesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg_hba.conf")
	if err := os.WriteFile(path, []byte("local all all trust\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestManagement(dir)
	res, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		FilePath: path,
		Content:  goodHBA,
	})
	if err != nil {
		t.Fatalf("UpdateConfig returned an error: %v", err)
	}
	if !res.Success {
		t.Fatalf("UpdateConfig refused a valid write: %s", res.ErrorMessage)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != goodHBA {
		t.Errorf("the file on disk is %q, want %q", got, goodHBA)
	}
}

// A config edit that cannot be undone is not a config edit an operator will
// make at 3am.
func TestUpdateConfigKeepsThePreviousContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg_hba.conf")
	previous := "local all all trust\n"
	if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestManagement(dir)
	if _, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		FilePath: path, Content: goodHBA,
	}); err != nil {
		t.Fatal(err)
	}

	backup, err := os.ReadFile(path + backupSuffix)
	if err != nil {
		t.Fatalf("no backup beside the replaced file: %v", err)
	}
	if string(backup) != previous {
		t.Errorf("the backup holds %q, want %q", backup, previous)
	}
}

// PostgreSQL refuses to start when its configuration is group- or
// world-readable, so a replacement that widens the mode is an outage on the
// next restart rather than now.
func TestUpdateConfigPreservesTheMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg_hba.conf")
	if err := os.WriteFile(path, []byte("local all all trust\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestManagement(dir)
	if _, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		FilePath: path, Content: goodHBA,
	}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode is %v, want 0600", perm)
	}
}

// The validator and the write are one decision: content that would lock every
// client out must not reach the disk, and the file already there must survive.
func TestUpdateConfigLeavesTheFileAloneWhenValidationFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg_hba.conf")
	previous := "local all all peer\n"
	if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestManagement(dir)
	res, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		FilePath: path,
		Content:  "# every rule commented out during the incident\n",
	})
	if err != nil {
		t.Fatalf("UpdateConfig returned an error: %v", err)
	}
	if res.Success {
		t.Fatal("a pg_hba.conf with no rules was written")
	}
	if !strings.Contains(res.ErrorMessage, "validation failed") {
		t.Errorf("the refusal does not say validation failed: %s", res.ErrorMessage)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != previous {
		t.Errorf("the existing file was modified: %q", got)
	}
	if _, err := os.Stat(path + backupSuffix); !os.IsNotExist(err) {
		t.Error("a refused write still left a backup")
	}
}

// The traversal guard, exercised through the RPC rather than the helper.
func TestUpdateConfigRefusesAPathOutsideTheAllowlist(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := newTestManagement(dir)
	res, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		// Has the allowed prefix, resolves outside it.
		FilePath: filepath.Join(dir, "..", filepath.Base(filepath.Dir(outside)), "victim"),
		Content:  "owned",
	})
	if err != nil {
		t.Fatalf("UpdateConfig returned an error: %v", err)
	}
	if res.Success {
		t.Fatal("a path outside the allowlist was written")
	}

	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "untouched" {
		t.Errorf("the file outside the allowlist was modified: %q", got)
	}
}

// A file that does not exist yet is created at initdb's mode rather than the
// process umask, and needs no backup.
func TestUpdateConfigCreatesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg_hba.conf")

	m := newTestManagement(dir)
	res, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		FilePath: path, Content: goodHBA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("creating a missing file was refused: %s", res.ErrorMessage)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the file was not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode is %v, want 0600", perm)
	}
	if _, err := os.Stat(path + backupSuffix); !os.IsNotExist(err) {
		t.Error("a file that did not exist still produced a backup")
	}
}

// Nothing may be left behind on the happy path: a stray temp file in the data
// directory is a file PostgreSQL did not put there and an operator has to
// reason about.
func TestUpdateConfigLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pg_hba.conf")

	m := newTestManagement(dir)
	if _, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		FilePath: path, Content: goodHBA,
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".pontus-") && !strings.HasSuffix(e.Name(), backupSuffix) {
			t.Errorf("a temporary file survived: %s", e.Name())
		}
	}
}

// A file this validator does not judge still gets the path guard and the write.
func TestUpdateConfigWritesAFileWithNoValidator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "postgresql.conf")

	m := newTestManagement(dir)
	res, err := m.UpdateConfig(context.Background(), &endpoints.UpdateConfigRequest{
		FilePath: path, Content: "max_connections = 200\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("an unjudged file was refused: %s", res.ErrorMessage)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "max_connections = 200\n" {
		t.Errorf("the file on disk is %q", got)
	}
}

func newTestManagement(dir string) *management {
	return NewManagement(
		[]string{dir},
		map[string]services.Validator{"pg_hba.conf": &validator.Postgres{}},
		nil, dir, "postgres",
	)
}
