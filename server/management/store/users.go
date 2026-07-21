package store

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/gsoultan/pontus/api/proto/endpoints"
	"github.com/gsoultan/pontus/pkg/auth"
)

type jsonUserStore struct {
	mu       sync.RWMutex
	filePath string
	users    map[string]*endpoints.LoginResponse // We reuse LoginResponse for user info or define a new message
}

func NewJSONUserStore(filePath string) (*jsonUserStore, error) {
	s := new(jsonUserStore{
		filePath: filePath,
		users:    make(map[string]*endpoints.LoginResponse),
	})

	if err := s.load(); err != nil {
		if os.IsNotExist(err) {
			// Add default admin if not exists?
			return s, nil
		}
		return nil, err
	}

	return s, nil
}

func (s *jsonUserStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &s.users)
}

func (s *jsonUserStore) save() error {
	data, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}

func (s *jsonUserStore) Get(username string) (*endpoints.LoginResponse, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[username]
	return u, ok
}

func (s *jsonUserStore) Upsert(username, password, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure password is hashed
	hashedPassword := password
	if !strings.HasPrefix(password, "$2a$") && !strings.HasPrefix(password, "$2b$") && !strings.HasPrefix(password, "$2y$") {
		var err error
		hashedPassword, err = auth.HashPassword(password)
		if err != nil {
			return err
		}
	}

	s.users[username] = new(endpoints.LoginResponse{
		Username: username,
		Role:     role,
		Token:    hashedPassword,
	})
	return s.save()
}

func (s *jsonUserStore) List() []*endpoints.LoginResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return slices.Collect(maps.Values(s.users))
}
