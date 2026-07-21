package service

import "github.com/kardianos/service"

type handler struct {
	onStart func() error
	onStop  func() error
}

// Start is called when the service is started.
func (h *handler) Start(s service.Service) error {
	go func() {
		if err := h.onStart(); err != nil {
			// In a real application, you might want to log this or handle it differently.
			_ = s.Stop()
		}
	}()
	return nil
}

// Stop is called when the service is stopped.
func (h *handler) Stop(s service.Service) error {
	return h.onStop()
}
