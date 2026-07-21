package insights

import (
	"context"
	"testing"

	"github.com/gsoultan/pontus/api/proto/domain"
)

func TestPostgres_Tune(t *testing.T) {
	tuner := NewPostgres()

	tests := []struct {
		name    string
		metrics *domain.SystemMetrics
		want    int // number of suggestions expected
	}{
		{
			name: "Standard 8GB RAM, 4 Cores",
			metrics: &domain.SystemMetrics{
				MemoryTotalBytes: 8 * 1024 * 1024 * 1024,
				CpuCores:         4,
			},
			want: 5,
		},
		{
			name: "Low RAM, 1 Core",
			metrics: &domain.SystemMetrics{
				MemoryTotalBytes: 1 * 1024 * 1024 * 1024,
				CpuCores:         1,
			},
			want: 5,
		},
		{
			name:    "Nil metrics",
			metrics: nil,
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := tuner.Tune(context.Background(), tt.metrics)
			if len(res.Suggestions) != tt.want {
				t.Errorf("Postgres.Tune() got %d suggestions, want %d", len(res.Suggestions), tt.want)
			}

			if tt.metrics != nil && tt.metrics.MemoryTotalBytes > 0 {
				// Check specific values for 8GB case
				if tt.name == "Standard 8GB RAM, 4 Cores" {
					foundShared := false
					for _, s := range res.Suggestions {
						if s.Parameter == "shared_buffers" && s.SuggestedValue == "2GB" {
							foundShared = true
						}
					}
					if !foundShared {
						t.Errorf("Postgres.Tune() did not suggest correct shared_buffers for 8GB RAM")
					}
				}
			}
		})
	}
}
