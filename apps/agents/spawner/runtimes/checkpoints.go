package runtimes

import (
	"context"

	"github.com/defos-ai/commander/framework/agent/action"
)

// UsesExecutionCheckpoints selects controller-owned persistence for actions.
// Missing or failed checkpoint access must never fall back to local records.
// Startup launches retain file records until bootstrap uses controller tasks.
func UsesExecutionCheckpoints(ctx context.Context) bool {
	_, exists := action.ExecutionFromContext(ctx)
	return exists
}
