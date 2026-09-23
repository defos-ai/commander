package runner

import (
	"context"
)

// Close suspends observation while preserving unfinished operations for restart.
// Cancel is the separate, explicit operation-level cancellation action.
// Close is safe to call again after a shutdown timeout.
func (r *Runner) Close(ctx context.Context) error {
	r.cancel()
	select {
	case <-r.done:
		return r.closeErr
	default:
	}
	select {
	case <-r.done:
		return r.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
