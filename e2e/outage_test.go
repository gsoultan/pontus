//go:build e2e

package e2e

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A backend that disappears mid-session must surface as an error on the next
// query, not as a hang.
//
// The client is already connected and the proxy is already holding a backend
// connection, so nothing in the acquisition path gets a chance to notice. This
// is the outage shape an operator actually meets, and the one where a proxy
// that stops answering is worse than no proxy at all.
func TestBackendLostMidSessionFailsTheQuery(t *testing.T) {
	link := newRelay(t, backendAddr())

	s := startStackWith(t, func(cfg string) string {
		cfg = replaceBackendAddr(cfg, link.addr())
		// Session pooling keeps the connection pinned to this client, so the
		// severed connection is the one the next query has to use.
		return setYAMLScalar(cfg, "pooling_mode", "session")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	conn := connect(t, ctx, s)
	var n int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("query before the outage: %v", err)
	}

	link.sever()

	queryCtx, queryCancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer queryCancel()

	start := time.Now()
	err := conn.QueryRow(queryCtx, "SELECT 2").Scan(&n)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a query succeeded after the backend was taken away")
	}
	// query_timeout is 30s in the harness config. Reaching it means the client
	// waited on a connection the proxy already knew was gone.
	if elapsed >= 30*time.Second {
		t.Errorf("the query hung for %v after the backend disappeared; "+
			"an outage should surface promptly", elapsed.Round(time.Second))
	}
	t.Logf("query failed %v after the outage: %v", elapsed.Round(time.Millisecond), err)
}

// A new session opened while the backend is down must be refused, and the
// proxy must recover once the backend returns.
//
// Recovery matters as much as failure: a proxy that fails correctly and then
// stays broken has only moved the outage.
func TestProxyRecoversWhenBackendReturns(t *testing.T) {
	link := newRelay(t, backendAddr())

	s := startStackWith(t, func(cfg string) string {
		return replaceBackendAddr(cfg, link.addr())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Healthy to begin with.
	first := connect(t, ctx, s)
	var n int
	if err := first.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
		t.Fatalf("query while healthy: %v", err)
	}

	link.sever()

	downCtx, downCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := connectErr(downCtx, s); err == nil {
		t.Error("a session was established while the backend was unreachable")
	}
	downCancel()

	// Bring it back at the same address.
	link.restore()

	// The health probe fails a node in ten seconds and deepCheck restores it,
	// so allow a couple of cycles.
	var recovered bool
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) && !recovered {
		attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if conn, err := connectErr(attemptCtx, s); err == nil {
			if err := conn.QueryRow(attemptCtx, "SELECT 3").Scan(&n); err == nil && n == 3 {
				recovered = true
			}
			_ = conn.Close(context.Background())
		}
		attemptCancel()
		if !recovered {
			time.Sleep(2 * time.Second)
		}
	}

	if !recovered {
		t.Error("the proxy never recovered after the backend came back")
	}
}

// The stream budget must refuse a new consumer with an explanation, not by
// dropping the connection.
//
// A CDC consumer that is simply disconnected retries forever against a proxy
// that will never accept it; one that is told why can be fixed.
func TestStreamBudgetRefusalExplainsItself(t *testing.T) {
	s := startStack(t)

	const budget = 4
	var held []net.Conn
	t.Cleanup(func() {
		for _, c := range held {
			_ = c.Close()
		}
	})

	for range budget {
		conn, err := net.DialTimeout("tcp", s.proxyAddr, 10*time.Second)
		if err != nil {
			t.Fatalf("dial proxy: %v", err)
		}
		_, _ = conn.Write(startupMessage(map[string]string{
			"user": backendUser(), "database": backendDB(), "replication": "database",
		}))
		held = append(held, conn)
	}

	// Give the proxy a moment to register them.
	time.Sleep(2 * time.Second)

	over, err := net.DialTimeout("tcp", s.proxyAddr, 10*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer over.Close()

	_, _ = over.Write(startupMessage(map[string]string{
		"user": backendUser(), "database": backendDB(), "replication": "database",
	}))

	response := readAll(t, over)
	if response == "" {
		t.Fatal("the consumer over budget was dropped without an explanation")
	}
	if !strings.Contains(response, "budget") {
		t.Errorf("refusal did not mention the budget, so an operator cannot act on it: %q",
			truncate(response))
	}
}

// The management API must keep working while the data plane is down.
//
// That is when an operator most needs it: a dashboard that goes dark during an
// outage cannot be used to diagnose the outage.
func TestManagementSurvivesBackendOutage(t *testing.T) {
	link := newRelay(t, backendAddr())

	s := startStackWith(t, func(cfg string) string {
		return replaceBackendAddr(cfg, link.addr())
	})

	token := s.login()
	projectID, proxyID := s.project(token)

	link.sever()

	out, code := s.rpc("GetStatus",
		map[string]string{"projectId": projectID, "proxyId": proxyID}, token)
	if code != http.StatusOK {
		t.Fatalf("GetStatus failed while the backend was down (%d): %v", code, out)
	}
	if _, ok := out["backends"]; !ok {
		t.Error("GetStatus returned no backends during an outage; the dashboard would show nothing")
	}
}
