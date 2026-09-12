//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A binary upgrade that does not drop connections.
//
// Without reuse_port the old process holds the port until it exits, so the new
// one cannot bind and the gap between them is an outage on the only port that
// matters. This measures the property that matters: while a second Pontus comes
// up on the same address and the first goes away, no client is refused.
func TestUpgradeServesThroughAHandover(t *testing.T) {
	requireBackend(t)

	old := startStackWith(t, func(cfg string) string {
		return setYAMLScalar(cfg, "reuse_port", "true")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// A client is already working before anything changes.
	if err := canQuery(ctx, old.proxyAddr); err != nil {
		t.Fatalf("the proxy did not serve before the upgrade: %v", err)
	}

	// Connections continue throughout, and every refusal is recorded.
	var attempts, refusals atomic.Int64
	var lastErr atomic.Value
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			attempts.Add(1)
			if err := canQuery(ctx, old.proxyAddr); err != nil {
				refusals.Add(1)
				lastErr.Store(err.Error())
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	// The new process binds the same address while the old one is still
	// serving. This is the step that is impossible without reuse_port.
	fresh := startStackOn(t, old.proxyAddr, old.dataDir, func(cfg string) string {
		return setYAMLScalar(cfg, "reuse_port", "true")
	})

	// Both are up. Let traffic flow across the pair.
	time.Sleep(2 * time.Second)

	// The old one drains and exits, as a deploy would do.
	old.stop()
	time.Sleep(2 * time.Second)

	close(stop)
	<-done

	if attempts.Load() < 20 {
		t.Fatalf("only %d connections were attempted; the test measured nothing", attempts.Load())
	}
	if n := refusals.Load(); n != 0 {
		t.Errorf("%d of %d connections were refused across the handover; last error: %v",
			n, attempts.Load(), lastErr.Load())
	}

	// The survivor is still serving.
	if err := canQuery(ctx, fresh.proxyAddr); err != nil {
		t.Errorf("the new proxy does not serve after the old one exited: %v", err)
	}
}

// Only one process runs orchestration, however many are serving.
//
// Sharing the port is safe for queries. It is not safe for failover: two
// managers on a five-second tick, each seeing no healthy primary, can both
// promote — the split brain the orchestration layer exists to prevent.
func TestOnlyOneProcessHoldsOrchestration(t *testing.T) {
	requireBackend(t)

	old := startStackWith(t, func(cfg string) string {
		return setYAMLScalar(cfg, "reuse_port", "true")
	})
	if !waitFor(30*time.Second, func() bool {
		return strings.Contains(old.logs.String(), "Holding orchestration")
	}) {
		t.Fatalf("the first process never claimed orchestration:\n%s", tailLog(old.logs.String(), 2000))
	}

	fresh := startStackOn(t, old.proxyAddr, old.dataDir, func(cfg string) string {
		return setYAMLScalar(cfg, "reuse_port", "true")
	})

	if !waitFor(30*time.Second, func() bool {
		return strings.Contains(fresh.logs.String(), "Not running orchestration")
	}) {
		t.Errorf("the second process did not stand down from orchestration:\n%s",
			tailLog(fresh.logs.String(), 2000))
	}
	if strings.Contains(fresh.logs.String(), "Holding orchestration") {
		t.Error("both processes claimed orchestration; two failover managers can both promote")
	}
}

// canQuery opens a session through the proxy and runs a statement.
func canQuery(ctx context.Context, proxyAddr string) error {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(dialCtx, fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		backendUser(), backendPass(), proxyAddr, backendDB()))
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	var n int
	return conn.QueryRow(dialCtx, "SELECT 1").Scan(&n)
}

// startStackOn runs a second Pontus bound to an address another one already
// holds, which is what an upgrade does.
//
// It takes the *same* data directory as the process it is replacing, because
// that is what an in-place upgrade is: the same installation, the same
// configuration, a new binary. It is also what scopes the orchestration lock —
// one claim per data directory, so two Pontus instances managing different
// clusters on one host still both orchestrate, while an upgrade of one does not
// run two failover managers over the same cluster.
func startStackOn(t *testing.T, proxyAddr, dataDir string, adjust func(string) string) *stack {
	t.Helper()

	root := repoRoot(t)
	binary := filepath.Join(t.TempDir(), "pontus")

	build := exec.Command("go", "build", "-o", binary, "./cmd/pontus")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pontus: %v\n%s", err, out)
	}

	// Only the management port is its own: the proxy port is the one being
	// taken over.
	ports, release := freePorts(t, 1)
	mgmtAddr := fmt.Sprintf("127.0.0.1:%d", ports[0])
	release()

	config := configYAML(dataDir, proxyAddr, mgmtAddr)
	if adjust != nil {
		config = adjust(config)
	}
	// Beside the binary, so it does not overwrite the running process's copy.
	configPath := filepath.Join(filepath.Dir(binary), "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	logs := &logSink{}
	cmd := exec.Command(binary, "-config", configPath)
	cmd.Dir = root
	cmd.Stdout = logs
	cmd.Stderr = logs
	cmd.Env = append(os.Environ(), "PONTUS_AUTH_KEY="+authKey)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start the second pontus: %v", err)
	}

	s := &stack{
		cmd: cmd, dataDir: dataDir, logs: logs, t: t,
		proxyAddr: proxyAddr, mgmtAddr: mgmtAddr,
	}
	t.Cleanup(s.stop)

	s.waitListening(mgmtAddr)
	return s
}
