package registry

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/pontus/api/proto/domain"
	"github.com/gsoultan/pontus/pkg/config"
)

// A project reaches the registry from the management store as well as from
// config.yaml, and the store path was never validated at startup. Before this,
// the handler switch ended in `default: NewPostgresHandler()`, so a project
// created through the API with a typo'd or unsupported protocol got a
// PostgreSQL proxy that came up healthy and framed every byte wrongly for the
// client that connected to it.
func TestCreateProjectStateRefusesAProtocolItCannotServe(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		defaults *config.Options
		want     error
	}{
		{
			name:     "a typo is refused rather than served as postgres",
			protocol: "postgre",
			want:     config.ErrUnknownProtocol,
		},
		{
			name:     "an unsupported protocol is refused",
			protocol: "mongodb",
			want:     config.ErrUnknownProtocol,
		},
		{
			name:     "mysql is refused without the opt-in",
			protocol: "mysql",
			want:     config.ErrMySQLExperimental,
		},
		{
			name:     "the opt-in is read from defaults, not assumed",
			protocol: "mysql",
			defaults: &config.Options{},
			want:     config.ErrMySQLExperimental,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Registry{defaults: tc.defaults}

			got, err := r.CreateProjectState(context.Background(),
				&domain.Project{Name: "p", Protocol: tc.protocol})

			if !errors.Is(err, tc.want) {
				t.Fatalf("CreateProjectState(protocol=%q) = %v, want %v",
					tc.protocol, err, tc.want)
			}
			if got != nil {
				t.Errorf("a refused project still produced state: %#v", got)
			}
		})
	}
}

// The refusal has to name the project, because an operator reading a startup
// failure has a list of them and no other way to tell which one is wrong.
func TestProtocolRefusalNamesTheProject(t *testing.T) {
	r := &Registry{}

	_, err := r.CreateProjectState(context.Background(),
		&domain.Project{Name: "analytics", Protocol: "mongodb"})
	if err == nil {
		t.Fatal("an unservable protocol was accepted")
	}
	if !errors.Is(err, config.ErrUnknownProtocol) {
		t.Fatalf("got %v, want ErrUnknownProtocol", err)
	}
	if !strings.Contains(err.Error(), "analytics") {
		t.Errorf("the refusal does not name the project: %v", err)
	}
}
