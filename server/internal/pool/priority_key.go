package pool

import (
	"context"
)

type priorityKey struct{}

// WithPriority adds priority to the context. 0: low, 1: normal, 2: high.
func WithPriority(ctx context.Context, priority int) context.Context {
	return context.WithValue(ctx, priorityKey{}, priority)
}

func getPriority(ctx context.Context) int {
	v := ctx.Value(priorityKey{})
	if v == nil {
		return 1 // Default to normal
	}
	return v.(int)
}
