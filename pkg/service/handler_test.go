package service

import (
	"testing"
	"time"
)

func TestHandler(t *testing.T) {
	ctx := t.Context()
	startCalled := make(chan struct{})
	stopCalled := make(chan struct{})

	h := new(handler{
		onStart: func() error {
			close(startCalled)
			return nil
		},
		onStop: func() error {
			close(stopCalled)
			return nil
		},
	})

	// Mock service.Service is not needed for these tests as we just call methods on h
	if err := h.Start(nil); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	select {
	case <-startCalled:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("onStart was not called")
	case <-ctx.Done():
		t.Error("test timed out")
	}

	if err := h.Stop(nil); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	select {
	case <-stopCalled:
		// Success
	case <-time.After(1 * time.Second):
		t.Error("onStop was not called")
	case <-ctx.Done():
		t.Error("test timed out")
	}
}
