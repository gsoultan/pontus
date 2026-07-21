package infrastructure

import (
	"strings"
	"testing"
)

func TestParsePackages(t *testing.T) {
	a := &aptManager{}
	input := `Package: postgresql-14
Version: 14.11-1.pgdg120+1
Architecture: amd64

Package: postgresql-15
Version: 15.6-1.pgdg120+1
Architecture: amd64

Package: postgresql-16
Version: 16.2-1.pgdg120+1
Architecture: amd64

Package: postgresql-16
Version: 16.1-1.pgdg120+1
Architecture: amd64
`
	versions, err := a.parsePackages(strings.NewReader(input))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if versions[14] != "14.11-1.pgdg120+1" {
		t.Errorf("expected v14 to be 14.11-1.pgdg120+1, got %s", versions[14])
	}
	if versions[15] != "15.6-1.pgdg120+1" {
		t.Errorf("expected v15 to be 15.6-1.pgdg120+1, got %s", versions[15])
	}
	if versions[16] != "16.2-1.pgdg120+1" {
		t.Errorf("expected v16 to be 16.2-1.pgdg120+1, got %s", versions[16])
	}
}

func TestCompareVersions(t *testing.T) {
	a := &aptManager{}
	tests := []struct {
		v1, v2   string
		expected int
	}{
		{"16.1", "16.2", -1},
		{"16.2", "16.2", 0},
		{"16.3", "16.2", 1},
		{"15.6-1", "15.6-2", -1},
	}

	for _, tt := range tests {
		result := a.compareVersions(tt.v1, tt.v2)
		if result != tt.expected {
			t.Errorf("compareVersions(%s, %s) = %d, expected %d", tt.v1, tt.v2, result, tt.expected)
		}
	}
}

func TestGetDistro(t *testing.T) {
	// This test depends on the environment, so we might want to skip it or mock it.
	// But let's see what happens.
	a := &aptManager{}
	distro, err := a.getDistro()
	if err != nil {
		t.Logf("getDistro failed (expected if not on Linux): %v", err)
		return
	}
	t.Logf("Detected distro: %s", distro)
}
