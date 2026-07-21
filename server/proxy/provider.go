package proxy

import (
	"context"
	"net"
)

// Provider defines the interface for the proxy service.
type Provider interface {
	// Serve starts accepting connections on the given listener.
	Serve(ctx context.Context, ln net.Listener) error
}
