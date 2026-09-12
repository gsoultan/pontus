package orchestration

import "testing"

// The port has to come from the address. This was a constant 5432 regardless of
// where the primary listened, so on any cluster not using the default port the
// replica was pointed at a port nothing served — and every caller is a recovery
// path, so it only ever failed during an incident.
func TestSplitHostPortUsesTheAddressPort(t *testing.T) {
	for _, tc := range []struct {
		addr string
		host string
		port int
	}{
		{"127.0.0.1:55843", "127.0.0.1", 55843},
		{"db1.internal:6432", "db1.internal", 6432},
		{"127.0.0.1:5432", "127.0.0.1", 5432},
		// A bare host keeps the default rather than failing a recovery on a
		// formatting detail.
		{"db1.internal", "db1.internal", defaultPostgresPort},
		{"db1.internal:notaport", "db1.internal", defaultPostgresPort},
	} {
		host, port := splitHostPort(tc.addr, defaultPostgresPort)
		if host != tc.host || port != tc.port {
			t.Errorf("splitHostPort(%q) = %q/%d, want %q/%d",
				tc.addr, host, port, tc.host, tc.port)
		}
	}
}

// The progress stream has no error field, so a stage name is the only way an
// agent can say it did not work. The alternative to recognising "failed" is
// treating it as progress.
func TestIsFailureStage(t *testing.T) {
	for _, stage := range []string{"Error", "error", "FAILED", " failure "} {
		if !isFailureStage(stage) {
			t.Errorf("isFailureStage(%q) = false, want true", stage)
		}
	}
	for _, stage := range []string{"Starting", "Syncing", "Done", ""} {
		if isFailureStage(stage) {
			t.Errorf("isFailureStage(%q) = true, want false", stage)
		}
	}
}
