package service

import (
	"context"

	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// Auth defines the interface for authentication operations.
type Auth interface {
	Login(ctx context.Context, req *endpoints.LoginRequest) (*endpoints.LoginResponse, error)
	CreateUser(ctx context.Context, req *endpoints.CreateUserRequest) (*endpoints.CreateUserResponse, error)
}
