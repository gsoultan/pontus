package infrastructure

// allowedCommands bounds what ExecuteCommand may run on a database host.
//
// The agent runs as root — it drives apt and systemctl — so this map is the
// whole boundary between "the control plane can orchestrate Postgres" and "the
// control plane owns the machine". A name earns a place here only if it fails
// all three tests:
//
//   - It cannot read an arbitrary file. The host holds .pgpass, the Pontus
//     config with admin_dsn in it, and /etc/shadow. `cat` and `tail` were here
//     and made every one of those readable over the wire, which is why they are
//     not anymore.
//   - It cannot re-exec the agent. `pontus-agent` was here, so a caller could
//     start a second agent with -insecure on another port and walk straight
//     around authentication. An allowlist that contains the guarded program
//     is not an allowlist.
//   - It is not a general-purpose interpreter or shell.
//
// The provisioner only needs pg_basebackup and pg_ctl; the rest are diagnostics
// and service control that the agent already exposes through dedicated RPCs, so
// listing them here adds no reach it did not already have.
//
// Everything else in the agent — apt-get, initdb, the restart path — builds its
// own argv in Go and never passes through here. Do not add a name because some
// runbook wants it; add an RPC that expresses the operation instead.
var allowedCommands = map[string]bool{
	// Replication and promotion — what the provisioner actually calls.
	"pg_basebackup": true,
	"pg_ctl":        true,

	// Backup and restore.
	"pg_dump":    true,
	"pg_restore": true,

	// Service control, already reachable via RestartService/ShutdownDatabase.
	"systemctl": true,
	"service":   true,

	// Read-only diagnostics that report on the process table and disk, not on
	// file contents.
	"pg_isready": true,
	"ps":         true,
	"df":         true,
	"free":       true,
}
