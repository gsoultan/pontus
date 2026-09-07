package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Backup, restore, vacuum and promotion.
//
// Each streaming operation here returns `(stream, nil)` after a validation
// failure and reports the reason as a stageError message. That reads like a
// swallowed error and is flagged as one, so: the transport does not carry an
// error raised while the stream is being constructed. Returning it reaches the
// caller as an empty stream with no reason at all, which is how a refusal
// arrived as "ended without completing it". The stream is the only channel that
// works, hence the nolint at each site.
//
// Each of these replaces a stub that emitted a progress bar and reported
// success without running anything. An operator who clicked "Backup" got a
// green tick and no backup, which is worse than an error: it is a disaster
// recovery plan that fails only when it is needed.
//
// The percentages here mark real transitions rather than a simulated timeline.
// pg_dump does not report progress, so inventing intermediate numbers would be
// the same lie in a smaller form.

// BackupDatabase dumps a database, or the whole cluster, to a file.
func (m *management) BackupDatabase(ctx context.Context, req *endpoints.BackupDatabaseRequest) (<-chan *endpoints.BackupProgress, error) {
	out := make(chan *endpoints.BackupProgress, 4)

	send := func(stage string, pct int32, format string, args ...any) {
		select {
		case out <- &endpoints.BackupProgress{
			Stage: stage, Percentage: pct, Message: fmt.Sprintf(format, args...),
		}:
		case <-ctx.Done():
		}
	}

	// The request is checked before the host. A caller who named an unusable
	// destination should be told that, not that this machine has no cluster —
	// the second is true of a misconfigured agent and says nothing about the
	// request that failed.
	err := validateBackupPath(req.BackupPath)
	var host *pgHost
	if err == nil {
		err = requireTool(dumpToolFor(req.Database))
	}
	if err == nil {
		host, err = resolveHost(m.clusterDir(""), m.dbUser)
	}
	if err != nil {
		go func() {
			defer close(out)
			send(stageError, 0, "%v", err)
		}()
		//nolint:nilerr // the reason has to travel on the stream; see the file comment
		return out, nil
	}

	go func() {
		defer close(out)

		tool := dumpToolFor(req.Database)
		send("Dumping", 10, "Running %s into %s", tool, req.BackupPath)

		var args []string
		if req.Database == "" {
			// The whole cluster, roles included. Plain SQL: pg_dumpall has no
			// custom format.
			args = []string{"--file=" + req.BackupPath}
		} else {
			// Custom format: compressed, and restorable selectively, which
			// plain SQL is not.
			args = []string{"--format=custom", "--file=" + req.BackupPath, "--dbname=" + req.Database}
		}

		if err := host.tool(ctx, tool, args...); err != nil {
			// A partial file is worse than none: it restores, and restores
			// wrong.
			_ = os.Remove(req.BackupPath)
			send(stageError, 0, "%s failed: %v", tool, err)
			return
		}

		size, err := os.Stat(req.BackupPath)
		if err != nil {
			send(stageError, 0, "%s reported success but wrote nothing to %s", tool, req.BackupPath)
			return
		}
		send("Done", 100, "Wrote %d bytes to %s", size.Size(), req.BackupPath)
	}()

	return out, nil
}

// dumpToolFor picks the dump program: the whole cluster needs pg_dumpall,
// which is the only one that carries roles and tablespaces.
func dumpToolFor(database string) string {
	if database == "" {
		return "pg_dumpall"
	}
	return "pg_dump"
}

// RestoreDatabase loads a dump back into a database.
func (m *management) RestoreDatabase(ctx context.Context, req *endpoints.RestoreDatabaseRequest) (<-chan *endpoints.RestoreProgress, error) {
	out := make(chan *endpoints.RestoreProgress, 4)

	send := func(stage string, pct int32, format string, args ...any) {
		select {
		case out <- &endpoints.RestoreProgress{
			Stage: stage, Percentage: pct, Message: fmt.Sprintf(format, args...),
		}:
		case <-ctx.Done():
		}
	}

	var err error
	if _, statErr := os.Stat(req.BackupPath); statErr != nil {
		err = fmt.Errorf("no backup at %s: %w", req.BackupPath, statErr)
	}
	var host *pgHost
	if err == nil {
		host, err = resolveHost(m.clusterDir(""), m.dbUser)
	}
	if err != nil {
		go func() {
			defer close(out)
			send(stageError, 0, "%v", err)
		}()
		//nolint:nilerr // the reason has to travel on the stream; see the file comment
		return out, nil
	}

	go func() {
		defer close(out)

		// The format decides the tool, and the file says which it is rather
		// than the caller: a custom-format dump handed to psql is executed as
		// SQL and fails on the first byte.
		custom, err := isCustomFormatDump(req.BackupPath)
		if err != nil {
			send(stageError, 0, "could not read %s: %v", req.BackupPath, err)
			return
		}

		tool := "psql"
		args := []string{"--file=" + req.BackupPath}
		if custom {
			tool = "pg_restore"
			args = []string{req.BackupPath}
		}
		if req.TargetDatabase != "" {
			args = append(args, "--dbname="+req.TargetDatabase)
		}
		if err := requireTool(tool); err != nil {
			send(stageError, 0, "%v", err)
			return
		}

		send("Restoring", 10, "Running %s from %s", tool, req.BackupPath)
		if err := host.tool(ctx, tool, args...); err != nil {
			send(stageError, 0, "%s failed: %v", tool, err)
			return
		}
		send("Done", 100, "Restored %s", req.BackupPath)
	}()

	return out, nil
}

// isCustomFormatDump reports whether a file is a pg_dump custom-format archive,
// which begins with the magic "PGDMP".
func isCustomFormatDump(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	// Read-only, so a failed close has nothing to report and nothing to lose.
	defer func() { _ = f.Close() }()

	magic := make([]byte, 5)
	n, err := f.Read(magic)
	if err != nil && n == 0 {
		// A file too short to hold the magic is simply not a custom archive.
		// Reporting EOF as a failure would turn "restore this empty file" into
		// "could not read it", which sends an operator looking at permissions.
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return string(magic[:n]) == "PGDMP", nil
}

// VacuumDatabase reclaims storage and refreshes planner statistics.
func (m *management) VacuumDatabase(ctx context.Context, req *endpoints.VacuumDatabaseRequest) (<-chan *endpoints.VacuumProgress, error) {
	out := make(chan *endpoints.VacuumProgress, 4)

	send := func(stage string, pct int32, format string, args ...any) {
		select {
		case out <- &endpoints.VacuumProgress{
			Stage: stage, Percentage: pct, Message: fmt.Sprintf(format, args...),
		}:
		case <-ctx.Done():
		}
	}

	host, err := resolveHost(m.clusterDir(""), m.dbUser)
	if err == nil {
		err = requireTool("vacuumdb")
	}
	if err != nil {
		go func() {
			defer close(out)
			send(stageError, 0, "%v", err)
		}()
		//nolint:nilerr // the reason has to travel on the stream; see the file comment
		return out, nil
	}

	go func() {
		defer close(out)

		args := []string{}
		if req.Database != "" {
			args = append(args, "--dbname="+req.Database)
		} else {
			args = append(args, "--all")
		}
		if req.Full {
			// Rewrites each table and takes an ACCESS EXCLUSIVE lock, so it is
			// only ever what the caller explicitly asked for.
			args = append(args, "--full")
		}
		if req.Analyze {
			args = append(args, "--analyze")
		}

		send("Vacuuming", 10, "Running vacuumdb %s", strings.Join(args, " "))
		if err := host.tool(ctx, "vacuumdb", args...); err != nil {
			send(stageError, 0, "vacuumdb failed: %v", err)
			return
		}
		send("Done", 100, "Vacuum complete")
	}()

	return out, nil
}

// PromoteNode promotes this host's standby to a primary.
//
// Pontus normally promotes over SQL with pg_promote(), which needs neither root
// nor the data directory. This is the fallback for when that is unavailable,
// and it used to `return Success: true` without running anything — so a
// failover that had already failed once reported success and left the cluster
// with no primary at all.
func (m *management) PromoteNode(ctx context.Context, req *endpoints.PromoteNodeRequest) (*endpoints.PromoteNodeResponse, error) {
	// The response carries the outcome, so a failure is a populated reply
	// rather than a transport error — the caller reads Success, and an RPC
	// error would reach it as "the call failed" with the reason discarded.
	host, err := resolveHost(m.clusterDir(""), m.dbUser)
	if err != nil {
		//nolint:nilerr // the outcome is the response's job; see above
		return &endpoints.PromoteNodeResponse{Success: false, ErrorMessage: err.Error()}, nil
	}
	if err := requireTool("pg_ctl"); err != nil {
		//nolint:nilerr // the outcome is the response's job; see above
		return &endpoints.PromoteNodeResponse{Success: false, ErrorMessage: err.Error()}, nil
	}

	args := []string{"-D", host.dataDir, "promote"}
	if wantsWait(req.WaitForCompletion) {
		// -w means a reply says the promotion finished, rather than that it was
		// accepted.
		args = append(args, "-w", "-t", "60")
	}

	if err := runTool(host.command(ctx, "pg_ctl", args...)); err != nil {
		return &endpoints.PromoteNodeResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("promoting the standby at %s: %v", host.dataDir, err),
		}, nil
	}
	return &endpoints.PromoteNodeResponse{Success: true}, nil
}

// wantsWait reads the request's wait flag, which is a string in the contract.
// Anything unrecognised waits: returning early from a promotion that has not
// finished is the answer that costs something.
func wantsWait(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "no", "0", "off":
		return false
	default:
		return true
	}
}

// validateBackupPath refuses a destination that cannot be written safely.
func validateBackupPath(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("no backup path given")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("backup path %q is not absolute; the agent's working "+
			"directory is not something a caller can reason about", path)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		return fmt.Errorf("backup directory %q does not exist", filepath.Dir(path))
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("backup path %q is a directory", path)
	}
	return nil
}
