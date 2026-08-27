package pool

import (
	"testing"
	"time"
)

// The queue bound has a config path, so it has to actually reach a pool.
// A setting that is parsed, documented and never read is the defect this
// project keeps finding; pkg/config's wiring test catches the unreferenced
// case, not the mis-wired one.
func TestWaitTimeoutIsConfigurable(t *testing.T) {
	t.Cleanup(func() { SetWaitTimeout(0) })

	if got := configuredWaitTimeout(); got != DefaultWaitTimeout {
		t.Fatalf("unset wait timeout is %v, want the %v default", got, DefaultWaitTimeout)
	}

	SetWaitTimeout(250 * time.Millisecond)
	if got := configuredWaitTimeout(); got != 250*time.Millisecond {
		t.Errorf("configured wait timeout is %v, want 250ms", got)
	}

	// A non-positive value means "unset", not "never wait" — zero would turn
	// every acquisition at the ceiling into an instant refusal.
	SetWaitTimeout(0)
	if got := configuredWaitTimeout(); got != DefaultWaitTimeout {
		t.Errorf("zero produced %v; it must restore the %v default", got, DefaultWaitTimeout)
	}

	SetWaitTimeout(-time.Second)
	if got := configuredWaitTimeout(); got != DefaultWaitTimeout {
		t.Errorf("a negative value produced %v; it must restore the default", got)
	}
}
