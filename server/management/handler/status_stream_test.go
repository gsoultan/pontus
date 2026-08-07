package handler

import (
	"testing"
	"time"
)

// interval_ms is client-supplied. Without a floor a dashboard could ask the
// server to rebuild the full status payload in a busy loop.
func TestClampStatusInterval(t *testing.T) {
	cases := []struct {
		name string
		ms   int32
		want time.Duration
	}{
		{"zero takes the default", 0, statusStreamDefaultInterval},
		{"negative takes the default", -1000, statusStreamDefaultInterval},
		{"below the floor is raised", 1, statusStreamMinInterval},
		{"a busy-loop request is raised", 0, statusStreamDefaultInterval},
		{"in range is honoured", 3000, 3 * time.Second},
		{"above the ceiling is capped", 600_000, statusStreamMaxInterval},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampStatusInterval(tc.ms); got != tc.want {
				t.Errorf("clampStatusInterval(%d) = %v, want %v", tc.ms, got, tc.want)
			}
		})
	}
}

func TestClampStatusIntervalNeverBelowFloor(t *testing.T) {
	for ms := int32(-100); ms <= 1000; ms += 7 {
		if got := clampStatusInterval(ms); got < statusStreamMinInterval {
			t.Fatalf("clampStatusInterval(%d) = %v, below the %v floor", ms, got, statusStreamMinInterval)
		}
	}
}
