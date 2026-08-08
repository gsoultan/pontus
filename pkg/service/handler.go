package service

import (
	"log/slog"

	"github.com/kardianos/service"
)

type handler struct {
	onStart func() error
	onStop  func() error
}

// Start is called when the service is started.
func (h *handler) Start(s service.Service) error {
	go func() {
		err := h.onStart()
		if err == nil {
			return
		}

		// Report it. This error used to be discarded, so a service that failed
		// to start — port already bound, bad config, missing credentials —
		// looked identical to one running normally: no output, no exit, just a
		// process sitting there. Under systemd it was worse, because the unit
		// stayed "active" while doing nothing.
		logger, logErr := s.Logger(nil)
		if logErr == nil {
			_ = logger.Error(err)
		}
		slog.Error("service failed to start", "error", err)

		_ = s.Stop()
	}()
	return nil
}

// Stop is called when the service is stopped.
func (h *handler) Stop(s service.Service) error {
	return h.onStop()
}
