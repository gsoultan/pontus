package main

import "testing"

// An agent started with -addr ":9091" is on the network whether or not anyone
// meant it to be, so the wildcard cases are the ones that matter.
func TestBindsLoopbackOnly(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:9091", true},
		{"localhost:9091", true},
		{"[::1]:9091", true},
		{"127.0.0.5:9091", true},

		// Every interface. This is the default shape and the dangerous one.
		{":9091", false},
		{"0.0.0.0:9091", false},
		{"[::]:9091", false},

		// A real address, and a name that cannot be answered.
		{"10.0.0.5:9091", false},
		{"db1.internal:9091", false},
		{"", false},
	} {
		if got := bindsLoopbackOnly(tc.addr); got != tc.want {
			t.Errorf("bindsLoopbackOnly(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
