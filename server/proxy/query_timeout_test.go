package proxy

import (
	"testing"
	"time"

	"github.com/gsoultan/pontus/pkg/config"
)

// The default has to survive an unset field, and an operator has to be able to
// turn the bound off. Zero cannot mean "off": zero is what an unset field
// already holds, so reading it that way would remove the bound from every
// deployment that never named the setting.
func TestQueryTimeoutConfigured(t *testing.T) {
	for _, tc := range []struct {
		name     string
		configed time.Duration
		want     time.Duration
		bounded  bool
	}{
		{"unset keeps the default", 0, 30 * time.Second, true},
		{"a value is honoured", 5 * time.Second, 5 * time.Second, true},
		{"a negative disables the bound", -1, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &Gateway{}
			g.queryTimeout = 30 * time.Second
			g.reconfigure(&config.Options{QueryTimeout: tc.configed})

			if g.queryTimeout != tc.want {
				t.Errorf("queryTimeout = %v, want %v", g.queryTimeout, tc.want)
			}

			ctx, cancel := g.withQueryTimeout(t.Context())
			defer cancel()
			if _, ok := ctx.Deadline(); ok != tc.bounded {
				t.Errorf("context has a deadline = %v, want %v", ok, tc.bounded)
			}
		})
	}
}
