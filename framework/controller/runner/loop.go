package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type taskResult struct {
	task   operations.Task
	output actions.ActionOutput
	err    error
}

// After startup recovery, this loop owns all mutable runner state. Workers only
// execute remote calls and return results; they never retain operationState.
func (r *Runner) run() {
	defer close(r.done)
	if r.ctx.Err() == nil {
		r.resumeTasks()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for r.ctx.Err() == nil {
		select {
		case <-r.ctx.Done():
		case request := <-r.requests:
			request()
		case result := <-r.results:
			r.acceptResult(result)
			r.signalScheduler()
		case <-r.wake:
			r.expireOperatorQuestions()
			r.dispatchQueued()
		case <-ticker.C:
			r.expireOperatorQuestions()
			r.dispatchQueued()
		}
	}
	// Join every launched call and persist successful results that arrive during
	// shutdown. Cancellation errors leave their durable tasks available for recovery.
	for r.workers > 0 {
		r.acceptResult(<-r.results)
	}
	for _, state := range r.states {
		if state.err != nil {
			r.closeErr = errors.Join(r.closeErr, fmt.Errorf("operation %s: %w", state.operation.ID, state.err))
		}
	}
}

func (r *Runner) acceptResult(result taskResult) {
	r.workers--
	r.finishTask(r.states[result.task.OperationID], result.task, result.output, result.err)
}

func submit[T any](ctx context.Context, r *Runner, execute func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if r.ctx.Err() != nil {
		return zero, ErrClosed
	}
	type response struct {
		value T
		err   error
	}
	reply := make(chan response, 1)
	request := func() {
		if err := ctx.Err(); err != nil {
			reply <- response{err: err}
			return
		}
		if r.ctx.Err() != nil {
			reply <- response{err: ErrClosed}
			return
		}
		requestCtx, cancel := r.requestContext(ctx)
		defer cancel()
		value, err := execute(requestCtx)
		reply <- response{value, err}
	}
	select {
	case r.requests <- request:
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-r.ctx.Done():
		return zero, ErrClosed
	}
	select {
	case result := <-reply:
		return result.value, result.err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-r.ctx.Done():
		return zero, ErrClosed
	}
}
