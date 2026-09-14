package manager

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/server/management/service"
)

// settingRecorder is a SettingProvider that remembers what was written, so a
// test can assert that nothing was.
type settingRecorder struct {
	stored map[string]string
	setErr error
}

func newSettingRecorder() *settingRecorder {
	return &settingRecorder{stored: make(map[string]string)}
}

func (s *settingRecorder) Get(_ context.Context, key string) (string, error) {
	return s.stored[key], nil
}

func (s *settingRecorder) Set(_ context.Context, key, value string) error {
	if s.setErr != nil {
		return s.setErr
	}
	s.stored[key] = value
	return nil
}

func (s *settingRecorder) Delete(_ context.Context, key string) error {
	delete(s.stored, key)
	return nil
}

func (s *settingRecorder) List(context.Context) ([]service.Setting, error) {
	out := make([]service.Setting, 0, len(s.stored))
	for k, v := range s.stored {
		out = append(out, service.Setting{Key: k, Value: v})
	}
	return out, nil
}

// The regression this file exists for. Settings were written to SQLite first
// and parsed afterwards, with the parse failure swallowed, so a value like
// query_timeout: "banana" was persisted, never applied, reported as Success,
// and rendered back by the dashboard as the current setting.
func TestSetClusterConfigPersistsNothingItCannotParse(t *testing.T) {
	tests := []struct {
		name  string
		param map[string]string
	}{
		{
			name:  "a duration that is not a duration",
			param: map[string]string{"query_timeout": "banana"},
		},
		{
			name:  "a duration with no unit",
			param: map[string]string{"query_timeout": "30"},
		},
		{
			name:  "a count that is not a number",
			param: map[string]string{"max_conns": "lots"},
		},
		{
			name:  "a negative connection ceiling",
			param: map[string]string{"max_conns": "-1"},
		},
		{
			name:  "a zero connection ceiling",
			param: map[string]string{"max_conns": "0"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newSettingRecorder()
			c := NewCluster(nil, store)

			res, err := c.SetClusterConfig(context.Background(),
				&endpoints.SetClusterConfigRequest{Parameters: tc.param})

			if err == nil {
				t.Fatalf("SetClusterConfig(%v) succeeded: %#v", tc.param, res)
			}
			if got := status.Code(err); got != codes.InvalidArgument {
				t.Errorf("code = %v, want InvalidArgument", got)
			}
			if len(store.stored) != 0 {
				t.Errorf("a value that could not be parsed was still persisted: %v", store.stored)
			}
		})
	}
}

// One bad parameter must not persist the good ones alongside it: a half-applied
// settings change is a state no operator asked for and none can see.
func TestSetClusterConfigIsAllOrNothing(t *testing.T) {
	store := newSettingRecorder()
	c := NewCluster(nil, store)

	_, err := c.SetClusterConfig(context.Background(),
		&endpoints.SetClusterConfigRequest{Parameters: map[string]string{
			"balancer":      "least_conns",
			"query_timeout": "not-a-duration",
		}})
	if err == nil {
		t.Fatal("a request with one unparseable parameter succeeded")
	}
	if len(store.stored) != 0 {
		t.Errorf("the valid parameters were persisted alongside the invalid one: %v", store.stored)
	}
}

// A store failure must reach the caller rather than being reported as success.
func TestSetClusterConfigReportsAStoreFailure(t *testing.T) {
	store := newSettingRecorder()
	store.setErr = errors.New("disk full")
	c := NewCluster(nil, store)

	if _, err := c.SetClusterConfig(context.Background(),
		&endpoints.SetClusterConfigRequest{Parameters: map[string]string{"balancer": "p2c"}}); err == nil {
		t.Fatal("a failed write was reported as success")
	}
}

// Parameters this manager does not interpret are still stored, because the
// settings store is read by more than the four keys applied to a live gateway.
func TestSetClusterConfigStoresUninterpretedParameters(t *testing.T) {
	store := newSettingRecorder()
	c := NewCluster(nil, store)

	res, err := c.SetClusterConfig(context.Background(),
		&endpoints.SetClusterConfigRequest{Parameters: map[string]string{
			"some_future_key": "value",
		}})
	if err != nil {
		t.Fatalf("an uninterpreted parameter was refused: %v", err)
	}
	if !res.Success {
		t.Error("Success is false for a stored parameter")
	}
	if store.stored["some_future_key"] != "value" {
		t.Errorf("the parameter was not stored: %v", store.stored)
	}
}
