package manager

import (
	"context"
	"errors"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	pwd "github.com/gsoultan/pontus/pkg/auth"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/service"
)

type auth struct {
	registry *registry.Registry
	issuer   *pwd.Issuer
}

// NewAuth creates a new auth manager.
//
// The issuer is constructed at startup and must be non-nil; there is no
// fallback secret. NewAuth previously defaulted to the literal
// "pontus-secret-key" when none was configured, which meant every deployment
// that forgot to set one shared a key published in this repository.
func NewAuth(r *registry.Registry, issuer *pwd.Issuer) service.Auth {
	return &auth{registry: r, issuer: issuer}
}

func (a *auth) Login(ctx context.Context, req *endpoints.LoginRequest) (*endpoints.LoginResponse, error) {
	if a.issuer == nil {
		return nil, errors.New("authentication is not configured")
	}

	u, ok := a.registry.UserStore().Get(req.Username)
	if !ok || !pwd.CheckPasswordHash(req.Password, u.Token) {
		// One message for both cases so the response cannot be used to
		// enumerate which usernames exist.
		return nil, errors.New("invalid username or password")
	}

	token, err := a.issuer.Issue(req.Username, u.Role)
	if err != nil {
		return nil, err
	}

	return &endpoints.LoginResponse{
		Token:    token,
		Role:     u.Role,
		Username: req.Username,
	}, nil
}

func (a *auth) CreateUser(ctx context.Context, req *endpoints.CreateUserRequest) (*endpoints.CreateUserResponse, error) {
	if req.Username == "" || req.Password == "" {
		return nil, errors.New("username and password are required")
	}

	role := req.Role
	if role == "" {
		role = "viewer"
	}

	if err := a.registry.UserStore().Upsert(req.Username, req.Password, role); err != nil {
		return nil, err
	}

	return &endpoints.CreateUserResponse{
		Username: req.Username,
		Role:     role,
	}, nil
}
