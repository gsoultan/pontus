package app

import (
	"context"
	"log"
	"path/filepath"

	"github.com/kardianos/service"
)

// PontusService implements service.Interface to allow running Pontus as a service.
type PontusService struct {
	runner Runner
	scfg   *service.Config
	cancel context.CancelFunc
}

// NewPontusService creates a new PontusService instance.
func NewPontusService(runner Runner, configPath string) *PontusService {
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}

	s := new(PontusService)
	s.runner = runner
	s.scfg = new(service.Config{
		Name:        "Pontus",
		DisplayName: "Pontus Server",
		Description: "Pontus Database Proxy and Management Server",
		Arguments:   []string{"-config", configPath},
	})
	return s
}

// Start is called when the service starts.
func (s *PontusService) Start(_ service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() {
		if err := s.runner.Run(ctx); err != nil {
			log.Printf("Service error: %v", err)
		}
	}()
	return nil
}

// Stop is called when the service stops.
func (s *PontusService) Stop(_ service.Service) error {
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

// HandleAction handles service management actions or runs the service.
func (s *PontusService) HandleAction(action string) error {
	svc, err := service.New(s, s.scfg)
	if err != nil {
		return err
	}

	if action != "" {
		return service.Control(svc, action)
	}

	return svc.Run()
}
