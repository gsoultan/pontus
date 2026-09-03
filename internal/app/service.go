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
//
// Drains before cancelling. This cancelled and returned, so a restart aborted
// every statement in flight and the process was gone milliseconds later — the
// drain that Gateway.Stop implements was never reached from a signal at all.
//
// The drain runs here rather than in Run's shutdown path because Run is on its
// own goroutine and nothing waits for it: once this returns, the service
// manager lets the process exit.
func (s *PontusService) Stop(_ service.Service) error {
	if drainer, ok := s.runner.(interface{ Drain() }); ok {
		drainer.Drain()
	}
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
