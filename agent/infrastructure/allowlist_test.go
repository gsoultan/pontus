package infrastructure

import (
	"context"
	"testing"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// The agent runs as root on a database host. These names were all reachable
// through ExecuteCommand and each one hands over something the RPC surface
// deliberately does not offer.
func TestExecuteCommandRefusesHostTakeoverPrimitives(t *testing.T) {
	m := &management{}

	for _, tc := range []struct {
		command string
		args    []string
		why     string
	}{
		{"cat", []string{"/etc/shadow"}, "arbitrary file read as root"},
		{"tail", []string{"-c", "+1", "/root/.pgpass"}, "arbitrary file read as root"},
		{"pontus-agent", []string{"-addr", ":9999", "-insecure"},
			"re-execs the agent without authentication"},
		{"sh", []string{"-c", "id"}, "shell"},
		{"bash", []string{"-c", "id"}, "shell"},
		{"env", nil, "runs an arbitrary binary"},
		{"echo", []string{"hi"}, "no production caller"},
		{"ls", []string{"/"}, "filesystem enumeration"},
		{"curl", []string{"http://example.com"}, "egress"},
	} {
		t.Run(tc.command, func(t *testing.T) {
			_, err := m.ExecuteCommand(context.Background(), &endpoints.ExecuteCommandRequest{
				Command: tc.command,
				Args:    tc.args,
			})
			if err == nil {
				t.Fatalf("ExecuteCommand allowed %q (%s)", tc.command, tc.why)
			}
		})
	}
}

// The orchestrator still has to do its job: set up a replica and promote it.
func TestExecuteCommandAllowsOrchestration(t *testing.T) {
	for _, command := range []string{"pg_basebackup", "pg_ctl"} {
		if !allowedCommands[command] {
			t.Errorf("%s is required by the provisioner but is not allowed", command)
		}
	}
}

// An empty command must be refused before it reaches exec, which would
// otherwise report a confusing lookup error.
func TestExecuteCommandRequiresACommand(t *testing.T) {
	m := &management{}
	if _, err := m.ExecuteCommand(context.Background(),
		&endpoints.ExecuteCommandRequest{}); err == nil {
		t.Fatal("an empty command was accepted")
	}
}
