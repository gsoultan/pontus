package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Info defines the interface for fetching server information.
type Info interface {
	GetServerInfo(ctx context.Context, req *endpoints.GetServerInfoRequest) (*endpoints.GetServerInfoResponse, error)
}
