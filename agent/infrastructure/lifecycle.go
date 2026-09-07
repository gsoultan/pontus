package infrastructure

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Creating and destroying a cluster.
//
// These replace stubs that printed a line and reported success. RemoveDatabase
// is the most destructive call the agent exposes — it is the one that deletes a
// database's storage — and it reported that it had done so without touching
// anything, which is the failure direction that makes an operator believe data
// is gone when it is not.

// RemoveDatabase stops this host's cluster and, when asked, deletes it.
func (m *management) RemoveDatabase(ctx context.Context, req *endpoints.RemoveDatabaseRequest) (*endpoints.RemoveDatabaseResponse, error) {
	host, err := resolveHost(m.clusterDir(req.DataDirectory), m.dbUser)
	if err != nil {
		//nolint:nilerr // the outcome is the response's job, as in PromoteNode
		return &endpoints.RemoveDatabaseResponse{Success: false, ErrorMessage: err.Error()}, nil
	}

	setup := &replicationSetup{dataDir: host.dataDir, runAs: host.runAs}
	if err := setup.stopPostgres(ctx); err != nil {
		//nolint:nilerr // the outcome is the response's job
		return &endpoints.RemoveDatabaseResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("stopping PostgreSQL at %s: %v", host.dataDir, err),
		}, nil
	}

	if !req.DeleteData {
		// Stopped but kept. Said plainly, because "success" on a request whose
		// whole subject is deletion should not leave a caller guessing whether
		// the storage is still there.
		return &endpoints.RemoveDatabaseResponse{
			Success:      true,
			ErrorMessage: fmt.Sprintf("stopped %s; data kept (delete_data was not set)", host.dataDir),
		}, nil
	}

	// The directory has already been confirmed to hold a PG_VERSION and to sit
	// well clear of the filesystem root — resolveHost refuses otherwise. The
	// contents go and the directory stays, so its ownership and permissions
	// survive for whatever is put there next.
	if err := emptyDir(host.dataDir); err != nil {
		//nolint:nilerr // the outcome is the response's job
		return &endpoints.RemoveDatabaseResponse{
			Success:      false,
			ErrorMessage: fmt.Sprintf("deleting the contents of %s: %v", host.dataDir, err),
		}, nil
	}

	return &endpoints.RemoveDatabaseResponse{Success: true}, nil
}

// emptyDir removes a directory's contents, keeping the directory itself.
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// InitializeDatabase creates a new cluster in an empty directory.
func (m *management) InitializeDatabase(ctx context.Context, req *endpoints.InitializeDatabaseRequest) (<-chan *endpoints.InitializeProgress, error) {
	out := make(chan *endpoints.InitializeProgress, 4)

	send := func(stage string, pct int32, format string, args ...any) {
		select {
		case out <- &endpoints.InitializeProgress{
			Stage: stage, Percentage: pct, Message: fmt.Sprintf(format, args...),
		}:
		case <-ctx.Done():
		}
	}

	plan, err := m.planInit(req)
	if err != nil {
		go func() {
			defer close(out)
			send(stageError, 0, "%v", err)
		}()
		//nolint:nilerr // the transport drops a construction error; see maintenance.go
		return out, nil
	}

	go func() {
		defer close(out)

		send("Init", 10, "Creating a cluster in %s as %s", plan.dataDir, plan.owner)
		if err := plan.initdb(ctx); err != nil {
			send(stageError, 0, "initdb failed: %v", err)
			return
		}

		if plan.database == "" {
			send("Done", 100, "Cluster created in %s", plan.dataDir)
			return
		}

		// The cluster has to be running before a database can be created in it,
		// and it is left running: a caller that asked for a database wants one
		// it can connect to.
		send("Starting", 70, "Starting the new cluster")
		if err := plan.start(ctx); err != nil {
			send(stageError, 0, "the new cluster did not start: %v", err)
			return
		}

		send("Creating", 85, "Creating database %s", plan.database)
		if err := plan.createDatabase(ctx); err != nil {
			send(stageError, 0, "creating database %s: %v", plan.database, err)
			return
		}
		send("Done", 100, "Cluster created in %s with database %s", plan.dataDir, plan.database)
	}()

	return out, nil
}

// initPlan is one cluster creation, resolved before anything is written.
type initPlan struct {
	dataDir  string
	owner    string
	database string
	password string
	binDir   string
	runAs    *syscall.Credential
}

// planInit validates the request and resolves everything needed to create a
// cluster.
func (m *management) planInit(req *endpoints.InitializeDatabaseRequest) (*initPlan, error) {
	dataDir := req.DataDirectory
	if dataDir == "" {
		dataDir = m.dataDir
	}
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("no data directory given, and this agent was started without -data-dir")
	}
	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("data directory %q is not an absolute path", dataDir)
	}

	// Never over an existing cluster.
	//
	// initdb refuses a non-empty directory, but only after the agent has
	// created and chowned it. Checking here means the answer to "initialise
	// over a live database" is a refusal that names the cluster, rather than a
	// tool error an operator has to interpret.
	if _, err := os.Stat(filepath.Join(dataDir, "PG_VERSION")); err == nil {
		return nil, fmt.Errorf("%q already holds a PostgreSQL cluster; "+
			"remove it deliberately before initialising another", dataDir)
	}

	owner := req.InitialUser
	if owner == "" {
		owner = m.dbUser
	}
	if owner == "" {
		owner = DefaultSuperuser
	}

	binDir, err := versionedBinDir(req.Version)
	if err != nil {
		return nil, err
	}

	plan := &initPlan{
		dataDir:  dataDir,
		owner:    owner,
		database: req.InitialDatabase,
		password: req.InitialPassword,
		binDir:   binDir,
	}
	if plan.runAs, err = accountCredential(owner); err != nil {
		return nil, err
	}
	return plan, nil
}

// versionedBinDir locates the tools for a specific major version.
//
// A host can carry several. Debian keeps them under
// /usr/lib/postgresql/<major>/bin, and taking whatever is first on PATH would
// initialise a cluster with one version's initdb that another version's server
// then refuses to start.
func versionedBinDir(version string) (string, error) {
	if strings.TrimSpace(version) == "" {
		return "", nil
	}

	major, _, _ := strings.Cut(version, ".")
	if _, err := strconv.Atoi(major); err != nil {
		return "", fmt.Errorf("%q is not a PostgreSQL version", version)
	}

	dir := filepath.Join("/usr/lib/postgresql", major, "bin")
	if _, err := os.Stat(filepath.Join(dir, "initdb")); err != nil {
		return "", fmt.Errorf("PostgreSQL %s is not installed on this host (%s has no initdb)", major, dir)
	}
	return dir, nil
}

// accountCredential resolves the OS account the tools must run as.
//
// initdb refuses to run as root outright, and the agent is root because it
// installs packages — so a named account is not optional here. Nil when the
// agent is not root, in which case it already runs as somebody who can own the
// cluster.
func accountCredential(name string) (*syscall.Credential, error) {
	if os.Geteuid() != 0 {
		return nil, nil
	}

	account, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("no account %q on this host to own the cluster: %w", name, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return nil, fmt.Errorf("account %q has a non-numeric uid %q", name, account.Uid)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return nil, fmt.Errorf("account %q has a non-numeric gid %q", name, account.Gid)
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, nil
}

// tool resolves a PostgreSQL binary, preferring the requested version's.
func (p *initPlan) tool(name string) string {
	if p.binDir == "" {
		return name
	}
	return filepath.Join(p.binDir, name)
}

func (p *initPlan) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, p.tool(name), args...)
	if p.runAs != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: p.runAs}
	}
	return cmd
}

// initdb creates the cluster, having first made a directory its owner can write.
func (p *initPlan) initdb(ctx context.Context) error {
	if err := os.MkdirAll(p.dataDir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", p.dataDir, err)
	}
	// initdb runs as the owner, so the directory has to belong to it before
	// the tool is invoked rather than after.
	if p.runAs != nil {
		if err := os.Chown(p.dataDir, int(p.runAs.Uid), int(p.runAs.Gid)); err != nil {
			return fmt.Errorf("giving %s to the cluster owner: %w", p.dataDir, err)
		}
	}

	args := []string{"--pgdata=" + p.dataDir, "--username=" + p.owner}
	if p.password != "" {
		// Through a file rather than an argument: a command line is readable by
		// every process on the host.
		pwFile := filepath.Join(p.dataDir, ".initdb-pw")
		if err := writePasswordFile(pwFile, p.password, p.runAs); err != nil {
			return err
		}
		// Removed on the way out. initdb has already read it, and a leftover
		// password file inside a data directory is the kind of thing that
		// survives for years.
		defer func() { _ = os.Remove(pwFile) }()
		args = append(args, "--auth-local=scram-sha-256", "--auth-host=scram-sha-256",
			"--pwfile="+pwFile)
	}

	return runTool(p.command(ctx, "initdb", args...))
}

// writePasswordFile writes a password where only the cluster's owner can read it.
func writePasswordFile(path, password string, runAs *syscall.Credential) error {
	if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
		return fmt.Errorf("writing the password file: %w", err)
	}
	if runAs != nil {
		if err := os.Chown(path, int(runAs.Uid), int(runAs.Gid)); err != nil {
			return fmt.Errorf("giving the password file to the cluster owner: %w", err)
		}
	}
	return nil
}

func (p *initPlan) start(ctx context.Context) error {
	return runTool(p.command(ctx, "pg_ctl", "-D", p.dataDir, "-w", "-t", "120", "start"))
}

// createDatabase adds the requested database to the new cluster.
func (p *initPlan) createDatabase(ctx context.Context) error {
	port, socketDir := connectionSettings(p.dataDir)

	args := []string{"--port=" + strconv.Itoa(port), "--username=" + p.owner}
	if socketDir != "" {
		args = append(args, "--host="+socketDir)
	}
	args = append(args, p.database)

	return runTool(p.command(ctx, "createdb", args...))
}

// ScheduleMaintenance is not implemented.
//
// It returned a fabricated task id and reported success, so a caller believed a
// recurring job existed that nothing would ever run. Recurring work needs a
// scheduler and somewhere to persist the schedule across restarts, neither of
// which the agent has — and inventing an id is worse than an honest refusal,
// because the refusal is visible the day it is set up rather than the day the
// job was supposed to fire.
func (m *management) ScheduleMaintenance(ctx context.Context, req *endpoints.ScheduleMaintenanceRequest) (*endpoints.ScheduleMaintenanceResponse, error) {
	return &endpoints.ScheduleMaintenanceResponse{
		Success: false,
		ErrorMessage: "scheduled maintenance is not implemented: the agent has no " +
			"scheduler and no store to keep a schedule across restarts. Drive " +
			"VacuumDatabase or BackupDatabase from cron or a systemd timer instead.",
	}, nil
}
