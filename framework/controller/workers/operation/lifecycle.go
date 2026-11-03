package operation

import (
	"context"
	"errors"
)

const (
	workerIdle uint32 = iota
	workerRunning
	workerStopped
)

// Run owns execution once per Worker. Recovery completes during construction;
// cancellation stops admission and joins calls before returning saved errors.
func (r *Worker) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("operation worker execution requires a context")
	}
	if !r.runState.CompareAndSwap(workerIdle, workerRunning) {
		if r.runState.Load() == workerStopped {
			return ErrClosed
		}
		return ErrAlreadyRunning
	}
	defer func() {
		r.cancel()
		r.runState.Store(workerStopped)
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
func (r *Worker) Close(ctx context.Context) error {
	r.cancel()
	if r.runState.CompareAndSwap(workerIdle, workerStopped) {
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
