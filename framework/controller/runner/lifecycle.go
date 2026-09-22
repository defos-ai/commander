package runner

import (
	"context"
	"errors"
	"fmt"
)

// Close suspends observation while preserving unfinished operations for restart.
// Cancel is the separate, explicit operation-level cancellation action.
// Close is safe to call again after a shutdown timeout.
func (r *Runner) Close(ctx context.Context) error {
	r.cancel()
	r.beginClose()
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

func (r *Runner) beginClose() {
	r.closeOnce.Do(func() { go r.finishClose() })
}

// finishClose may wait for a request holding r.mu while it writes to storage.
// It runs independently of Close so the caller's shutdown deadline stays usable.
func (r *Runner) finishClose() {
	r.mu.Lock()
	r.closed = true
	states := make([]*operationState, 0, len(r.states))
	for _, s := range r.states {
		states = append(states, s)
	}
	r.mu.Unlock()
	r.wg.Wait()
	for _, s := range states {
		s.mu.Lock()
		if s.err != nil {
			r.closeErr = errors.Join(r.closeErr, fmt.Errorf("operation %s: %w", s.operation.ID, s.err))
		}
		s.mu.Unlock()
	}
	close(r.done)
}
