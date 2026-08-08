//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/api/proto/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const agentToken = "e2e-agent-token"

// buildAgent compiles cmd/agent once per test that needs it.
func buildAgent(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "pontus-agent")
	build := exec.Command("go", "build", "-o", binary, "./cmd/agent")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent: %v\n%s", err, out)
	}
	return binary
}

// startAgent runs the agent with a token and returns its address.
func startAgent(t *testing.T) string {
	t.Helper()

	binary := buildAgent(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	logs := &strings.Builder{}
	cmd := exec.Command(binary, "-addr", addr)
	cmd.Stdout = logs
	cmd.Stderr = logs
	cmd.Env = append(os.Environ(), "PONTUS_AGENT_TOKEN="+agentToken)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start agent: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		if t.Failed() {
			t.Logf("agent logs:\n%s", logs.String())
		}
	})

	waitDial(t, cmd, addr, logs)
	return addr
}

// waitDial blocks until the agent accepts a connection, failing fast if the
// process exits first rather than burning the whole timeout.
func waitDial(t *testing.T, cmd *exec.Cmd, addr string, logs *strings.Builder) {
	t.Helper()

	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			conn.Close()
			return
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("agent exited before listening on %s\n%s", addr, logs.String())
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for the agent on %s\n%s", addr, logs.String())
}

// agentClient dials the agent and attaches the token as authorization metadata,
// which is what orchestration.NewAgentClient does on the wire.
func agentClient(t *testing.T, addr, token string) (service.AgentServiceClient, context.Context, func()) {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", token)
	}

	return service.NewAgentServiceClient(conn), ctx, func() {
		cancel()
		_ = conn.Close()
	}
}

// The agent installs database software and promotes nodes as root. Starting it
// without credentials used to leave all of that reachable by anyone who could
// open a socket, because the interceptor was attached only when a token
// happened to be set.
func TestAgentRefusesToStartWithoutAToken(t *testing.T) {
	binary := buildAgent(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	cmd := exec.Command(binary, "-addr", addr)
	// Explicitly strip the variable in case the developer running the suite has
	// it exported.
	cmd.Env = append(os.Environ(), "PONTUS_AGENT_TOKEN=")

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("the agent started with no token and no -insecure")
	}
	if !strings.Contains(string(out), "token is required") {
		t.Errorf("the agent exited but did not say why: %q", string(out))
	}
}

// A caller with no credentials, or the wrong ones, must be refused.
func TestAgentRejectsUnauthenticatedCalls(t *testing.T) {
	addr := startAgent(t)

	for name, token := range map[string]string{
		"no token":    "",
		"wrong token": "not-the-token",
		"prefix":      agentToken[:5],
	} {
		t.Run(name, func(t *testing.T) {
			client, ctx, done := agentClient(t, addr, token)
			defer done()

			_, err := client.GetSystemInfo(ctx, &endpoints.GetSystemInfoRequest{})
			if err == nil {
				t.Fatal("the agent answered an unauthorized caller")
			}
			if got := status.Code(err); got != codes.Unauthenticated {
				t.Errorf("code = %v, want Unauthenticated (err: %v)", got, err)
			}
		})
	}
}

func TestAgentAcceptsTheConfiguredToken(t *testing.T) {
	addr := startAgent(t)

	client, ctx, done := agentClient(t, addr, agentToken)
	defer done()

	if _, err := client.GetSystemInfo(ctx, &endpoints.GetSystemInfoRequest{}); err != nil {
		t.Fatalf("the agent rejected the configured token: %v", err)
	}
}

// Authentication is not the only boundary: an authenticated caller still must
// not be able to run anything it likes on a database host.
func TestAgentRefusesCommandsOffTheAllowlist(t *testing.T) {
	addr := startAgent(t)

	for _, command := range []string{"cat", "tail", "sh", "pontus-agent"} {
		t.Run(command, func(t *testing.T) {
			client, ctx, done := agentClient(t, addr, agentToken)
			defer done()

			stream, err := client.ExecuteCommand(ctx, &endpoints.ExecuteCommandRequest{
				Command: command,
				Args:    []string{"/etc/hosts"},
			})
			if err != nil {
				return // refused at the call, which is the point
			}

			// A stream that opens must still carry no output.
			for {
				msg, err := stream.Recv()
				if err != nil {
					return
				}
				if msg.Stdout != "" {
					t.Fatalf("%s ran and returned output: %q", command, truncate(msg.Stdout))
				}
			}
		})
	}
}
