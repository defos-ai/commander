package runner

import (
	"context"
	"errors"
)

const (
	runnerIdle uint32 = iota
	runnerRunning
	runnerStopped
)

// Run owns execution once per Runner. Recovery completes during construction;
// cancellation stops admission and joins calls before returning saved errors.
func (r *Runner) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("runner execution requires a context")
	}
	if !r.runState.CompareAndSwap(runnerIdle, runnerRunning) {
		if r.runState.Load() == runnerStopped {
			return ErrClosed
		}
		return ErrAlreadyRunning
	}
	defer func() {
		r.cancel()
		r.runState.Store(runnerStopped)
		close(r.done)
	}()
	stop := context.AfterFunc(ctx, r.cancel)
	defer stop()
	if ctx.Err() != nil {
		r.cancel()
	}
	r.run()
	return r.closeErr
}

// Close suspends observation while preserving unfinished operations for restart.
// Cancel is the separate, explicit operation-level cancellation action.
// Close is safe to call again after a shutdown timeout.
func (r *Runner) Close(ctx context.Context) error {
	r.cancel()
	if r.runState.CompareAndSwap(runnerIdle, runnerStopped) {
		close(r.done)
	}
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
