//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pgjdbc is a fourth independent implementation — pure Java, sharing no code
// with libpq, pgx or asyncpg — and it is what most enterprise deployments use.
//
// It runs only where a JDK exists. macOS ships a `java` stub that reports
// "Unable to locate a Java Runtime", so presence on PATH is not enough and is
// checked by running it. A skip here means UNVERIFIED, not fine: the whole
// argument for a driver matrix is that asyncpg found a hang the other two
// tolerated.
//
//	brew install openjdk        # or any JDK
//	curl -o /tmp/pontus-drivers/jdbc/postgresql.jar \
//	  https://repo1.maven.org/maven2/org/postgresql/postgresql/42.7.4/postgresql-42.7.4.jar
func TestJDBCAuthenticatesAgainstPontus(t *testing.T) {
	java := workingJava(t)
	jar := jdbcDriver(t)

	s := authStackOnLoopback(t)
	port := proxyPort(t, s)

	dir := t.TempDir()
	source := filepath.Join(dir, "Probe.java")

	program := fmt.Sprintf(`
import java.sql.*;

public class Probe {
    public static void main(String[] args) throws Exception {
        String url = "jdbc:postgresql://127.0.0.1:%s/%s";
        try (Connection c = DriverManager.getConnection(url, %q, %q)) {
            // Prepared statements, because that is how pgjdbc runs everything
            // and where a wire implementation is actually tested.
            try (PreparedStatement ps = c.prepareStatement("SELECT ?::int")) {
                for (int i = 0; i < 5; i++) {
                    ps.setInt(1, i);
                    try (ResultSet rs = ps.executeQuery()) {
                        if (!rs.next() || rs.getInt(1) != i) {
                            throw new IllegalStateException("wrong value at " + i);
                        }
                    }
                }
            }
            try (Statement st = c.createStatement();
                 ResultSet rs = st.executeQuery("SELECT current_user")) {
                rs.next();
                System.out.println("jdbc-ok " + rs.getString(1));
            }
        }
    }
}
`, port, backendDB(), backendUser(), backendPass())

	if err := os.WriteFile(source, []byte(program), 0o600); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Single-file source mode, so no separate compile step is needed.
	cmd := exec.CommandContext(ctx, java, "-cp", jar, source)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pgjdbc could not use Pontus: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "jdbc-ok") {
		t.Fatalf("unexpected reply from pgjdbc: %s", out)
	}
	t.Logf("jdbc: %s", strings.TrimSpace(string(out)))
}

// workingJava finds a JDK that actually runs, not just a launcher stub.
func workingJava(t *testing.T) string {
	t.Helper()

	candidates := []string{os.Getenv("PONTUS_E2E_JAVA"), "java"}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = exec.CommandContext(ctx, path, "-version").Run()
		cancel()
		if err == nil {
			return path
		}
	}

	t.Skip("no working JDK — pgjdbc is UNVERIFIED on this machine. " +
		"Install one (brew install openjdk) or set PONTUS_E2E_JAVA")
	return ""
}

// jdbcDriver locates the pgjdbc jar.
func jdbcDriver(t *testing.T) string {
	t.Helper()

	candidates := []string{
		os.Getenv("PONTUS_E2E_PGJDBC"),
		"/tmp/pontus-drivers/jdbc/postgresql.jar",
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.Size() > 0 {
			return candidate
		}
	}

	t.Skip("pgjdbc jar not found — see the comment above this test")
	return ""
}

// authStackOnLoopback is the Pontus-authenticated stack on its default address.
func authStackOnLoopback(t *testing.T) *stack {
	t.Helper()
	requireBackend(t)

	return startStackWith(t, func(cfg string) string {
		return cfg + `
auth:
  mode: pontus
  cache_ttl: 30s
`
	})
}
