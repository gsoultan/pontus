package manager

import (
	"context"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gsoultan/pontus/api/proto/endpoints"
	pwd "github.com/gsoultan/pontus/pkg/auth"
	"github.com/gsoultan/pontus/server/management/infrastructure/registry"
	"github.com/gsoultan/pontus/server/management/service"
)

type auth struct {
	registry  *registry.Registry
	jwtSecret string
}

// NewAuth creates a new auth manager.
func NewAuth(r *registry.Registry, jwtSecret string) service.Auth {
	if jwtSecret == "" {
		jwtSecret = "pontus-secret-key"
	}
	return &auth{
		registry:  r,
		jwtSecret: jwtSecret,
	}
}

func (a *auth) Login(ctx context.Context, req *endpoints.LoginRequest) (*endpoints.LoginResponse, error) {
	u, ok := a.registry.UserStore().Get(req.Username)
	if !ok || !pwd.CheckPasswordHash(req.Password, u.Token) {
		return nil, errors.New("invalid username or password")
	}

	role := u.Role

	// Generate JWT token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": req.Username,
		"role":     role,
		"exp":      time.Now().Add(time.Hour * 24).Unix(),
	})

	// Use the configured secret
	tokenString, err := token.SignedString([]byte(a.jwtSecret))
	if err != nil {
		return nil, err
	}

	return &endpoints.LoginResponse{
		Token:    tokenString,
		Role:     role,
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
