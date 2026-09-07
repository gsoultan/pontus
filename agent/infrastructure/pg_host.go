package infrastructure

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gsoultan/pontus/pkg/system"
)

// pgHost is the local PostgreSQL cluster an administrative command acts on:
// where it lives, how to reach it, and who the tools must run as.
//
// The connection is over the server's own unix socket rather than TCP, and no
// password is carried. That is not a shortcut — it is the only credential the
// agent legitimately has. It runs on the database host as root, so it can
// become the cluster's owner, and that account authenticates locally by peer or
// trust. Asking the control plane to ship a password to a process that can
// already read the data directory would add a secret to the wire and buy
// nothing.
type pgHost struct {
	dataDir   string
	port      int
	socketDir string
	user      string
	runAs     *syscall.Credential
}

// DefaultSuperuser is the role the client tools connect as when none is
// configured.
//
// "postgres" is the near-universal convention — initdb names the bootstrap
// superuser after the OS account that ran it, and every distribution runs it as
// postgres. It is a default rather than an assumption: -db-user overrides it,
// because a cluster initialised by hand may well be owned by something else.
const DefaultSuperuser = "postgres"

// resolveHost locates the cluster and everything needed to run a tool against
// it.
func resolveHost(dataDir, user string) (*pgHost, error) {
	if dataDir == "" {
		dataDir = system.DetectPostgresDataDir()
	}
	if err := validateDataDir(dataDir); err != nil {
		return nil, err
	}

	cred, err := ownerCredential(dataDir)
	if err != nil {
		return nil, err
	}

	if user == "" {
		user = DefaultSuperuser
	}

	h := &pgHost{dataDir: dataDir, user: user, runAs: cred}
	h.port, h.socketDir = connectionSettings(dataDir)
	return h, nil
}

// defaultPort is used when the cluster's configuration names none, which means
// PostgreSQL's own compiled-in default is in force.
const defaultPort = 5432

// connectionSettings reads the port and socket directory the cluster is
// actually running with.
//
// Read rather than assumed: a host running two clusters has at most one of them
// on 5432, and the socket directory differs by distribution — /tmp where
// PostgreSQL was built from source or by Homebrew, /var/run/postgresql on
// Debian. Connecting to the wrong one reports "no such file or directory" and
// looks like the server is down.
func connectionSettings(dataDir string) (port int, socketDir string) {
	port = defaultPort

	settings := map[string]string{}
	// postgresql.auto.conf last: it wins at load time, so it holds the value in
	// force.
	for _, name := range []string{"postgresql.conf", "postgresql.auto.conf"} {
		content, err := readFileString(filepath.Join(dataDir, name))
		if err != nil {
			continue
		}
		for setting, value := range connectionSettingsIn(content) {
			settings[setting] = value
		}
	}

	if raw, ok := settings["port"]; ok {
		if n, err := strconv.Atoi(unquote(raw)); err == nil && n > 0 && n <= 65535 {
			port = n
		}
	}
	if raw, ok := settings["unix_socket_directories"]; ok {
		// The setting is a list; the first entry is the one to dial.
		first, _, _ := strings.Cut(unquote(raw), ",")
		socketDir = strings.TrimSpace(first)
	}
	return port, socketDir
}

// connectionSettingsNames are the settings that say how to reach the server.
var connectionSettingsNames = []string{"port", "unix_socket_directories"}

func connectionSettingsIn(content string) map[string]string {
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
		if !contains(connectionSettingsNames, name) {
			continue
		}
		if idx := strings.Index(value, "#"); idx >= 0 {
			value = value[:idx]
		}
		if value = strings.TrimSpace(value); value != "" {
			found[name] = value
		}
	}
	return found
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// connArgs are the flags that point a client tool at this cluster.
func (h *pgHost) connArgs() []string {
	args := []string{"--port=" + strconv.Itoa(h.port)}
	if h.socketDir != "" {
		args = append(args, "--host="+h.socketDir)
	}
	if h.user != "" {
		// Named explicitly: without it the tools connect as the *OS* account
		// the agent runs the command as, which on a database host is rarely a
		// role that exists.
		args = append(args, "--username="+h.user)
	}
	return args
}

// command builds a tool invocation that runs as the cluster's owner.
func (h *pgHost) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if h.runAs != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: h.runAs}
	}
	return cmd
}

// tool runs a client tool against this cluster, with the connection flags
// already applied.
func (h *pgHost) tool(ctx context.Context, name string, args ...string) error {
	return runTool(h.command(ctx, name, append(h.connArgs(), args...)...))
}

// requireTool reports a missing binary as its own error.
//
// "executable file not found" reaches an operator as a failed backup with no
// hint that the client package simply is not installed, which is a different
// problem from a backup that ran and failed.
func requireTool(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is not installed on this host, so this operation "+
			"cannot run; install the PostgreSQL client tools", name)
	}
	return nil
}
