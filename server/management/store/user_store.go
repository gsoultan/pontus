package store

import (
	"github.com/gsoultan/pontus/api/proto/endpoints"
)

// User defines the interface for user management persistence.
type User interface {
	List() []*endpoints.LoginResponse
	Get(username string) (*endpoints.LoginResponse, bool)
	Upsert(username, password, role string) error
}
