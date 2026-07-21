package infrastructure

import (
	"testing"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

func TestService_UpdateConfig(t *testing.T) {
	svc := NewService()
	ctx := t.Context()

	tests := []struct {
		name     string
		filePath string
		content  string
		want     bool
	}{
		{
			name:     "Invalid path",
			filePath: "/etc/shadow",
			content:  "something",
			want:     false,
		},
		{
			name:     "Disallowed path",
			filePath: "/tmp/test.conf",
			content:  "something",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.UpdateConfig(ctx, &endpoints.UpdateConfigRequest{
				FilePath: tt.filePath,
				Content:  tt.content,
			})
			if err != nil {
				t.Fatalf("UpdateConfig failed: %v", err)
			}
			if resp.Success != tt.want {
				t.Errorf("UpdateConfig() success = %v, want %v. Error: %s", resp.Success, tt.want, resp.ErrorMessage)
			}
		})
	}
}

func TestService_GetSystemInfo(t *testing.T) {
	svc := NewService()
	ctx := t.Context()

	resp, err := svc.GetSystemInfo(ctx)
	if err != nil {
		t.Fatalf("GetSystemInfo failed: %v", err)
	}

	if resp.Hostname == "" {
		t.Error("Expected non-empty hostname")
	}
	if resp.Os == "" {
		t.Error("Expected non-empty OS")
	}
}
