//go:build e2e

package e2e

import (
	"strings"
	"sync"
)

// logSink collects a child process's output for a test to read.
//
// exec.Cmd writes stdout and stderr from goroutines it owns, while the test
// goroutine reads the same buffer — to scrape the bootstrap password, or to
// attach the log to a failure message. A strings.Builder was used here and that
// is a data race by construction: the suite failed under -race in whichever
// test happened to read while the proxy was still logging, which looked like
// flakiness rather than the harness bug it was.
type logSink struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logSink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
