package service

import (
	"fmt"

	"github.com/kardianos/service"
)

type osManager struct {
	svc service.Service
}

// NewManager creates a new OS service manager.
func NewManager(cfg Config, onStart, onStop func() error) (Manager, error) {
	sConfig := new(service.Config{
		Name:        cfg.Name,
		DisplayName: cfg.DisplayName,
		Description: cfg.Description,
		Arguments:   cfg.Arguments,
		Executable:  cfg.Executable,
	})

	h := new(handler{
		onStart: onStart,
		onStop:  onStop,
	})

	s, err := service.New(h, sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create service: %w", err)
	}

	return new(osManager{svc: s}), nil
}

func (m *osManager) Install() error {
	return m.svc.Install()
}

func (m *osManager) Uninstall() error {
	return m.svc.Uninstall()
}

func (m *osManager) Start() error {
	return m.svc.Start()
}

func (m *osManager) Stop() error {
	return m.svc.Stop()
}

func (m *osManager) Status() (string, error) {
	status, err := m.svc.Status()
	if err != nil {
		return "", err
	}
	switch status {
	case service.StatusRunning:
		return "Running", nil
	case service.StatusStopped:
		return "Stopped", nil
	default:
		return "Unknown", nil
	}
}

func (m *osManager) Run() error {
	return m.svc.Run()
}
