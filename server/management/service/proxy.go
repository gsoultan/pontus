package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Proxy defines the interface for proxy management.
type Proxy interface {
	AddProxy(ctx context.Context, req *endpoints.AddProxyRequest) (*endpoints.AddProxyResponse, error)
	RemoveProxy(ctx context.Context, req *endpoints.RemoveProxyRequest) (*endpoints.RemoveProxyResponse, error)
	UpdateProxy(ctx context.Context, req *endpoints.UpdateProxyRequest) (*endpoints.UpdateProxyResponse, error)
}
