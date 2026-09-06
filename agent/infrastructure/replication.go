package infrastructure

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/system"
)

// Rebuilding a node as a replica of another.
//
// This is the primitive every recovery path depends on — split-brain healing,
// follow_primary and auto_rejoin all reduce to it — and it is the one that
// discards a data directory, so the guards here are the feature rather than
// decoration around it.
//
// It replaces a stub that slept three times and reported "Replication
// configured" at 100% having touched nothing, which made every one of those
// recovery paths a no-op that logged success.

// stageError is the stage name a caller reads as failure. The progress message
// has no error field, so the stage is the only channel for saying it did not
// work; `orchestration.isFailureStage` matches this.
const stageError = "Error"

// replicationSetup is one rebuild, with everything it needs resolved up front.
type replicationSetup struct {
	dataDir  string
	primary  string
	port     int
	user     string
	password string
	slot     string
	rewind   bool

	// runAs is the uid/gid the PostgreSQL tools must run as. Zero means "run as
	// this process", which is correct when the agent is not root.
	runAs *syscall.Credential

	// identity is the node's own address settings, captured before the rebuild
	// and restored after it. A base backup copies the *primary's*
	// configuration, so without this the rebuilt node comes back trying to bind
	// the primary's port.
	identity map[string]string
}

// identitySettings are the settings that say which server this is rather than
// how it behaves. They must survive a rebuild; everything else is the
// primary's to dictate, which is the point of copying it.
var identitySettings = []string{"port", "listen_addresses"}

// SetupReplication rebuilds this host's PostgreSQL as a streaming replica of
// the primary named in the request.
func (m *management) SetupReplication(ctx context.Context, req *endpoints.SetupReplicationRequest) (<-chan *endpoints.ReplicationProgress, error) {
	out := make(chan *endpoints.ReplicationProgress, 8)

	setup, err := m.planReplication(req)
	if err != nil {
		// Reported through the stream rather than as a returned error.
		//
		// The transport does not carry an error raised while the stream is
		// being set up: the caller saw an empty stream and reported "ended
		// without completing it", which is true and says nothing about why.
		// The stage is the channel that reaches an operator.
		go func() {
			defer close(out)
			out <- &endpoints.ReplicationProgress{Stage: stageError, Message: err.Error()}
		}()
		return out, nil
	}

	go func() {
		defer close(out)

		emit := func(stage string, pct int32, format string, args ...any) {
			message := fmt.Sprintf(format, args...)

			// Logged as well as streamed. A rebuild can run for half an hour on
			// a host an operator is watching directly, and the caller's view of
			// it is a single line at the end saying whether it worked.
			if stage == stageError {
				slog.Error("Rebuild failed", "data_dir", setup.dataDir, "reason", message)
			} else {
				slog.Info("Rebuild", "stage", stage, "data_dir", setup.dataDir, "detail", message)
			}

			select {
			case out <- &endpoints.ReplicationProgress{
				Stage: stage, Percentage: pct, Message: message,
			}:
			case <-ctx.Done():
			}
		}
		fail := func(format string, args ...any) {
			emit(stageError, 0, format, args...)
		}

		// Copy first, destroy last.
		//
		// The obvious order — stop, empty the directory, copy into it — leaves a
		// window minutes long where the node holds nothing and only this process
		// knows how to refill it. If the agent dies in that window the node is
		// gone. It is not a hypothetical: the agent and the database share a
		// lifecycle whenever the database runs in a container, where PostgreSQL
		// is PID 1 and stopping it takes the agent down mid-rebuild.
		//
		// Staging the copy alongside the cluster and swapping it in reduces that
		// window to two renames. Anything that fails before the swap leaves the
		// original data directory exactly as it was.
		if err := setup.clearStaging(); err != nil {
			fail("could not clear the staging directory %s: %v", setup.stagingDir(), err)
			return
		}

		// Captured before anything is destroyed. A base backup brings the
		// primary's postgresql.conf with it, so a node rebuilt without this
		// comes back trying to bind the primary's port — which is either taken,
		// or worse, free.
		setup.captureIdentity()

		rewound := false
		if setup.rewind {
			// pg_rewind is the cheap path — it copies only the blocks that
			// diverged — but it works in place, so it needs the server stopped
			// and it has no staging step to fall back on. It is also fragile:
			// it needs a clean shutdown and either data checksums or
			// wal_log_hints. A failure falls through to the full copy.
			emit("Stopping", 15, "Stopping PostgreSQL at %s", setup.dataDir)
			if err := setup.stopPostgres(ctx); err != nil {
				fail("could not stop PostgreSQL at %s: %v", setup.dataDir, err)
				return
			}
			emit("Rewinding", 30, "Trying pg_rewind against %s", setup.primaryAddr())
			if err := setup.pgRewind(ctx); err != nil {
				emit("Rewinding", 35, "pg_rewind did not apply (%v); taking a base backup instead", err)
			} else {
				rewound = true
			}
		}

		if !rewound {
			emit("Syncing", 40, "Taking a base backup from %s into %s",
				setup.primaryAddr(), setup.stagingDir())
			if err := setup.baseBackup(ctx); err != nil {
				_ = setup.clearStaging()
				fail("base backup from %s failed: %v", setup.primaryAddr(), err)
				return
			}

			emit("Stopping", 70, "Stopping PostgreSQL at %s", setup.dataDir)
			if err := setup.stopPostgres(ctx); err != nil {
				_ = setup.clearStaging()
				fail("could not stop PostgreSQL at %s: %v", setup.dataDir, err)
				return
			}

			emit("Swapping", 75, "Replacing the cluster with the new copy")
			if err := setup.swapInStaging(); err != nil {
				fail("could not put the new copy in place at %s: %v", setup.dataDir, err)
				return
			}
		}

		emit("Configuring", 80, "Writing standby configuration")
		if err := setup.writeStandbyConfig(); err != nil {
			fail("could not write the standby configuration: %v", err)
			return
		}

		emit("Starting", 90, "Starting PostgreSQL")
		if err := setup.startPostgres(ctx); err != nil {
			fail("PostgreSQL did not start after the rebuild: %v", err)
			return
		}

		// The caller verifies that the node actually streams. What is checked
		// here is only what this process can see without credentials: the
		// server came back up, and it came back as a standby.
		if _, err := os.Stat(filepath.Join(setup.dataDir, "standby.signal")); err != nil {
			fail("PostgreSQL started but standby.signal is missing, so it came back as a primary")
			return
		}

		// The superseded cluster is kept until the replacement is proven, so a
		// failed start leaves something to go back to.
		setup.discardPrevious()

		emit("Done", 100, "Now following %s", setup.primaryAddr())
	}()

	return out, nil
}

// planReplication validates the request and resolves everything the rebuild
// needs, before any of it is destructive.
func (m *management) planReplication(req *endpoints.SetupReplicationRequest) (*replicationSetup, error) {
	if req.PrimaryHost == "" {
		return nil, fmt.Errorf("no primary host to follow")
	}
	port := int(req.PrimaryPort)
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("primary port %d is not a port", req.PrimaryPort)
	}

	dataDir := req.DataDirectory
	if dataDir == "" {
		dataDir = system.DetectPostgresDataDir()
	}
	if err := validateDataDir(dataDir); err != nil {
		return nil, err
	}

	// Refuse a rebuild this process cannot survive.
	//
	// Stopping PostgreSQL is a step in the middle of the sequence. Where the
	// database is PID 1 — every database-in-a-container deployment — stopping
	// it tears down the container and takes the agent with it, so the rebuild
	// ends there every time. Staging keeps the node's data safe when that
	// happens, but the node is left down and the caller retries into the same
	// wall. Saying so is more useful than a stop that never returns.
	if name, ok := initProcessName(); ok && isPostgresProcess(name) {
		return nil, fmt.Errorf("refusing to rebuild: PostgreSQL is this host's init "+
			"process (pid 1 is %q), so stopping it would terminate this agent "+
			"mid-rebuild; run the agent outside the database's container, or "+
			"supervise both under an init that outlives the database", name)
	}

	cred, err := ownerCredential(dataDir)
	if err != nil {
		return nil, err
	}

	return &replicationSetup{
		dataDir:  dataDir,
		primary:  req.PrimaryHost,
		port:     port,
		user:     req.ReplicationUser,
		password: req.ReplicationPassword,
		slot:     req.SlotName,
		rewind:   req.UseRewind,
		runAs:    cred,
	}, nil
}

func (s *replicationSetup) primaryAddr() string {
	return s.primary + ":" + strconv.Itoa(s.port)
}

// conninfo is the libpq string the standby will use to reach its primary.
func (s *replicationSetup) conninfo() string {
	parts := []string{
		"host=" + s.primary,
		"port=" + strconv.Itoa(s.port),
	}
	if s.user != "" {
		parts = append(parts, "user="+s.user)
	}
	if s.password != "" {
		parts = append(parts, "password="+s.password)
	}
	return strings.Join(parts, " ")
}

// validateDataDir refuses anything that is not recognisably a PostgreSQL data
// directory.
//
// The next step empties this path. A mistyped or undetected directory would be
// deleted just as willingly as the right one, so the check is not "does it
// exist" but "does it contain the file only a data directory has".
func validateDataDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("no data directory given and none could be detected")
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("data directory %q is not an absolute path", dir)
	}
	if filepath.Clean(dir) == "/" {
		return fmt.Errorf("refusing to treat / as a data directory")
	}
	// A data directory two levels from the root is far more likely to be a
	// mount point than a cluster.
	if strings.Count(filepath.Clean(dir), string(filepath.Separator)) < 2 {
		return fmt.Errorf("refusing %q as a data directory: it is too close to the filesystem root", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "PG_VERSION")); err != nil {
		return fmt.Errorf("%q does not contain PG_VERSION, so it is not a PostgreSQL data directory", dir)
	}
	return nil
}

// ownerCredential is the identity the PostgreSQL tools must run as.
//
// The agent is root, because it installs packages and manages services. The
// tools must not be: pg_ctl refuses to run as root outright, and a
// pg_basebackup run as root fills the data directory with files the server
// cannot then read. Running as the directory's own owner is both correct and
// independent of what the account happens to be called.
func ownerCredential(dir string) (*syscall.Credential, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot stat %s: %w", dir, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Not a POSIX filesystem; run as this process and let the tools object.
		return nil, nil
	}
	if os.Geteuid() != 0 || int(stat.Uid) == os.Geteuid() {
		// Only root can change identity, and there is nothing to change to when
		// the agent already runs as the owner.
		return nil, nil
	}
	return &syscall.Credential{Uid: stat.Uid, Gid: stat.Gid}, nil
}

// command builds a PostgreSQL tool invocation that runs as the data
// directory's owner and can authenticate to the primary.
func (s *replicationSetup) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+s.password)
	if s.runAs != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: s.runAs}
	}
	return cmd
}

func runTool(cmd *exec.Cmd) error {
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(cmd.Path), err, condense(string(out)))
	}
	return nil
}

// condense trims tool output to something a log line can carry.
func condense(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no output)"
	}
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return strings.ReplaceAll(s, "\n", "; ")
}

// stopPostgres halts the server. A server that is already down is the expected
// state on a node that just crashed, not an error.
func (s *replicationSetup) stopPostgres(ctx context.Context) error {
	if !s.isRunning(ctx) {
		return nil
	}
	// -m fast rather than immediate: this node is about to be rebuilt, but a
	// clean shutdown is what makes pg_rewind possible at all.
	if err := runTool(s.command(ctx, "pg_ctl", "-D", s.dataDir, "-m", "fast", "-w", "-t", "60", "stop")); err != nil {
		return err
	}
	if s.isRunning(ctx) {
		return fmt.Errorf("PostgreSQL is still running after pg_ctl stop")
	}
	return nil
}

func (s *replicationSetup) startPostgres(ctx context.Context) error {
	return runTool(s.command(ctx, "pg_ctl", "-D", s.dataDir, "-w", "-t", "120", "start"))
}

func (s *replicationSetup) isRunning(ctx context.Context) bool {
	return s.command(ctx, "pg_ctl", "-D", s.dataDir, "status").Run() == nil
}

// pgRewind rewinds this cluster onto the primary's timeline.
func (s *replicationSetup) pgRewind(ctx context.Context) error {
	source := fmt.Sprintf("host=%s port=%d", s.primary, s.port)
	if s.user != "" {
		source += " user=" + s.user
	}
	return runTool(s.command(ctx, "pg_rewind",
		"--target-pgdata="+s.dataDir, "--source-server="+source, "--progress"))
}

// stagingDir is where a new copy is assembled before it replaces the cluster.
func (s *replicationSetup) stagingDir() string { return s.dataDir + ".pontus-rebuild" }

// previousDir is where the superseded cluster waits until the replacement has
// started successfully.
func (s *replicationSetup) previousDir() string { return s.dataDir + ".pontus-previous" }

// clearStaging removes anything an interrupted rebuild left behind.
func (s *replicationSetup) clearStaging() error {
	if err := os.RemoveAll(s.stagingDir()); err != nil {
		return err
	}
	return os.RemoveAll(s.previousDir())
}

// swapInStaging moves the new copy into place, keeping the old cluster until
// the replacement is proven.
//
// Two renames within one filesystem, so the window in which the node holds no
// data directory is as short as it can be made.
func (s *replicationSetup) swapInStaging() error {
	if err := os.Rename(s.dataDir, s.previousDir()); err != nil {
		return fmt.Errorf("moving the old cluster aside: %w", err)
	}
	if err := os.Rename(s.stagingDir(), s.dataDir); err != nil {
		// Put it back rather than leaving the node with no data directory at
		// all: the old cluster is stale, but it is a cluster.
		if restoreErr := os.Rename(s.previousDir(), s.dataDir); restoreErr != nil {
			return fmt.Errorf("could not install the new cluster (%w) and could not "+
				"restore the old one (%v); the data directory is at %s",
				err, restoreErr, s.previousDir())
		}
		return fmt.Errorf("installing the new cluster: %w", err)
	}
	return nil
}

// discardPrevious removes the superseded cluster once the replacement is
// running. Failure is not fatal — it costs disk, not correctness.
func (s *replicationSetup) discardPrevious() {
	_ = os.RemoveAll(s.previousDir())
}

// baseBackup copies the primary into the staging directory.
//
// Into staging rather than over the live cluster: pg_basebackup needs an empty
// target, and emptying the real data directory first is what turns an
// interrupted rebuild into a destroyed node.
func (s *replicationSetup) baseBackup(ctx context.Context) error {
	args := []string{
		"--pgdata=" + s.stagingDir(),
		"--host=" + s.primary,
		"--port=" + strconv.Itoa(s.port),
		// -R writes standby.signal and primary_conninfo, so the copy comes back
		// as a standby rather than as a second primary.
		"--write-recovery-conf",
		// Stream WAL alongside the copy; without it a long backup can outrun
		// the primary's retention and produce a replica that cannot catch up.
		"--wal-method=stream",
		"--checkpoint=fast",
		"--no-password",
	}
	if s.user != "" {
		args = append(args, "--username="+s.user)
	}
	if s.slot != "" {
		args = append(args, "--slot="+s.slot)
	}
	return runTool(s.command(ctx, "pg_basebackup", args...))
}

// writeStandbyConfig makes sure the cluster comes back as a standby following
// the right primary.
//
// pg_basebackup -R has already written both of these, but pg_rewind has not,
// and a cluster rewound without them starts as a primary on a timeline it has
// just abandoned. Writing them unconditionally is what makes the two paths
// converge.
func (s *replicationSetup) writeStandbyConfig() error {
	signal := filepath.Join(s.dataDir, "standby.signal")
	if err := writeAsOwner(signal, nil, s.dataDir); err != nil {
		return fmt.Errorf("writing standby.signal: %w", err)
	}

	// ALTER SYSTEM territory, so it is appended to postgresql.auto.conf rather
	// than to postgresql.conf, which an operator owns.
	autoConf := filepath.Join(s.dataDir, "postgresql.auto.conf")
	existing, err := os.ReadFile(autoConf)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	line := fmt.Sprintf("primary_conninfo = '%s'\n", strings.ReplaceAll(s.conninfo(), "'", "''"))
	content := stripSetting(string(existing), "primary_conninfo") + line
	if s.slot != "" {
		content = stripSetting(content, "primary_slot_name") +
			fmt.Sprintf("primary_slot_name = '%s'\n", s.slot)
	}

	// Restore who this server is. postgresql.auto.conf wins over
	// postgresql.conf, so writing them here overrides whatever the base backup
	// brought from the primary.
	for _, setting := range identitySettings {
		value, ok := s.identity[setting]
		if !ok {
			continue
		}
		content = stripSetting(content, setting) +
			fmt.Sprintf("%s = %s\n", setting, value)
	}
	return writeAsOwner(autoConf, []byte(content), s.dataDir)
}

// captureIdentity reads the settings that say which server this is, so they can
// be restored over the primary's after the copy.
//
// postgresql.auto.conf is read last because it wins at load time, so the value
// it holds is the one in force.
func (s *replicationSetup) captureIdentity() {
	s.identity = map[string]string{}
	for _, name := range []string{"postgresql.conf", "postgresql.auto.conf"} {
		content, err := os.ReadFile(filepath.Join(s.dataDir, name))
		if err != nil {
			continue
		}
		for setting, value := range settingsIn(string(content)) {
			s.identity[setting] = value
		}
	}
}

// settingsIn extracts the identity settings from a configuration file.
func settingsIn(content string) map[string]string {
	found := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if !slices.Contains(identitySettings, name) {
			continue
		}
		// Trailing comments are ordinary in a generated postgresql.conf.
		if idx := strings.Index(value, "#"); idx >= 0 {
			value = value[:idx]
		}
		if value = strings.TrimSpace(value); value != "" {
			found[name] = value
		}
	}
	return found
}

// stripSetting removes any existing assignment of a setting, so appending does
// not leave two lines whose last-wins order is an accident.
func stripSetting(content, setting string) string {
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), setting) {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// writeAsOwner writes a file and gives it the data directory's ownership, so a
// root-run agent does not leave files the server cannot read.
func writeAsOwner(path string, content []byte, dataDir string) error {
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}

	info, err := os.Stat(dataDir)
	if err != nil {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 {
		return nil
	}
	return os.Chown(path, int(stat.Uid), int(stat.Gid))
}

// initProcessName reads the name of PID 1, reporting whether it could.
//
// Linux only, via /proc. Elsewhere the question does not arise: the case this
// guards is a container whose single process is the database.
func initProcessName() (string, bool) {
	comm, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(comm)), true
}

// isPostgresProcess reports whether a process name is the database.
func isPostgresProcess(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "postgres" || name == "postmaster"
}
