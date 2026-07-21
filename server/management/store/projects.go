package store

import (
	"encoding/json"
	"maps"
	"os"
	"slices"
	"sync"

	"github.com/gsoultan/pontus/api/proto/domain"
)

type jsonProjectStore struct {
	mu       sync.RWMutex
	filePath string
	projects map[string]*domain.Project
}

func NewJSONProjectStore(filePath string) (*jsonProjectStore, error) {
	s := &jsonProjectStore{
		filePath: filePath,
		projects: make(map[string]*domain.Project),
	}

	if err := s.load(); err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}

	return s, nil
}

func (s *jsonProjectStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &s.projects)
}

func (s *jsonProjectStore) save() error {
	data, err := json.MarshalIndent(s.projects, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}

func (s *jsonProjectStore) List() []*domain.Project {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return slices.Collect(maps.Values(s.projects))
}

func (s *jsonProjectStore) Get(id string) (*domain.Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.projects[id]
	return p, ok
}

func (s *jsonProjectStore) Upsert(p *domain.Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.projects[p.Id] = p
	return s.save()
}

func (s *jsonProjectStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.projects, id)
	return s.save()
}
