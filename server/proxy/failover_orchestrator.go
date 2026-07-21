package proxy

import "context"

type FailoverOrchestrator interface {
	TriggerFailover(ctx context.Context) error
}
