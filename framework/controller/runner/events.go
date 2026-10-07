package runner

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/operations"
)

type operationView struct {
	operation operations.Operation
	changed   <-chan struct{}
	stopped   bool
}

func (r *Runner) view(operationID string) (operationView, error) {
	state, err := r.state(operationID)
	if err != nil {
		return operationView{}, err
	}
	if state.err != nil {
		return operationView{}, state.err
	}
	return operationView{operation: state.operation.Clone(), changed: state.changed}, nil
}

func (r *Runner) snapshot(ctx context.Context, operationID string) (operationView, error) {
	view, err := submit(ctx, r, func(context.Context) (operationView, error) {
		return r.view(operationID)
	})
	if errors.Is(err, ErrClosed) {
		select {
		case <-r.done:
			// The loop has joined its workers; its final snapshots are immutable.
			view, err = r.view(operationID)
			view.stopped = true
		case <-ctx.Done():
			return operationView{}, ctx.Err()
		}
	}
	if errors.Is(err, operations.ErrUnknownOperation) {
		op, err := r.store.Get(ctx, operationID)
		return operationView{operation: op, stopped: true}, err
	}
	return view, err
}

// Events waits outside the coordination loop, so a long poll cannot block writes.
func (r *Runner) Events(ctx context.Context, operationID string, after uint64, wait bool) ([]operations.Event, error) {
	for {
		view, err := r.snapshot(ctx, operationID)
		if err != nil {
			return nil, err
		}
		events, err := eventsAfter(view.operation, after)
		if err != nil {
			return nil, err
		}
		finished := view.operation.Status.Terminal() && !runningTasks(view.operation)
		if len(events) > 0 || !wait || finished || view.stopped {
			return events, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-view.changed:
		case <-r.done:
		}
	}
}

func (r *Runner) WaitTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	for {
		view, err := r.snapshot(ctx, operationID)
		if err != nil {
			return operations.Task{}, err
		}
		i := taskIndex(&view.operation, taskID)
		if i < 0 {
			return operations.Task{}, errors.New("unknown task")
		}
		task := view.operation.Tasks[i]
		if task.Status != operations.TaskRunning && task.Status != operations.TaskQueued {
			return task, nil
		}
		if view.stopped {
			return operations.Task{}, ErrNotRunning
		}
		select {
		case <-ctx.Done():
			return operations.Task{}, ctx.Err()
		case <-view.changed:
		case <-r.done:
		}
	}
}

func eventsAfter(operation operations.Operation, after uint64) ([]operations.Event, error) {
	if after > uint64(len(operation.Events)) {
		return nil, errors.New("event cursor exceeds the latest sequence")
	}
	return append([]operations.Event{}, operation.Events[after:]...), nil
}
