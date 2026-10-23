package action

import (
	"context"

	"github.com/defos-ai/commander/framework/comms/messages"
)

// Execution identifies the current dispatch independently of action input.
// It correlates retries; it does not establish authorization or process ownership.
type Execution struct {
	CommandID string
	Work      messages.WorkContext
}

type executionContextKey struct{}

// WithExecution supplies execution identity to an executor. Agent supplies its
// current command and work context automatically when executing an action.
func WithExecution(ctx context.Context, execution Execution) context.Context {
	return context.WithValue(ctx, executionContextKey{}, execution)
}

func ExecutionFromContext(ctx context.Context) (Execution, bool) {
	execution, ok := ctx.Value(executionContextKey{}).(Execution)
	return execution, ok
}
