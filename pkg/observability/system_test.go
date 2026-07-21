package observability

import (
	"testing"
)

func TestCollectSystemMetrics(t *testing.T) {
	data := CollectSystemMetrics()

	// On most systems, these should be non-zero or at least not error out.
	// We don't check for exact values as they depend on the environment.

	if data.MemoryTotalBytes == 0 {
		t.Log("Warning: MemoryTotalBytes is 0, which might be expected in some CI environments but usually should be positive")
	}

	// CPU usage can be 0 if the system is completely idle, but let's check it's within range
	if data.CPUUsagePercent < 0 || data.CPUUsagePercent > 100 {
		t.Errorf("CPUUsagePercent out of range: %f", data.CPUUsagePercent)
	}

	if data.MemoryUsagePercent < 0 || data.MemoryUsagePercent > 100 {
		t.Errorf("MemoryUsagePercent out of range: %f", data.MemoryUsagePercent)
	}
}
