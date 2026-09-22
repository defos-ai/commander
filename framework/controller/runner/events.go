package runner

import (
	"context"
	"errors"

	"commander/framework/domain/operations"
)

// Events returns updates after a sequence cursor. With wait=true it waits for
// the next update, caller cancellation, or a terminal operation with no tasks left.
// Finished operations outside the active runtime return their stored history.
func (r *Runner) Events(ctx context.Context, operationID string, after uint64, wait bool) ([]operations.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := r.state(operationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		operation, err := r.store.Get(ctx, operationID)
		if err != nil {
			return nil, err
		}
		return eventsAfter(operation, after)
	}
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return nil, err
		}
		events, err := eventsAfter(s.operation, after)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		finished := s.operation.Status.Terminal() && !runningTasks(s.operation)
		select {
		case <-r.done:
			finished = true
		default:
		}
		changed := s.changed
		s.mu.Unlock()
		if len(events) > 0 || !wait || finished {
			return events, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		case <-r.done:
			// Recheck the final snapshot after shutdown's last persistence write.
		}
	}
}

func (r *Runner) WaitTask(ctx context.Context, operationID, taskID string) (operations.Task, error) {
	if err := ctx.Err(); err != nil {
		return operations.Task{}, err
	}
	s, err := r.state(operationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		operation, err := r.store.Get(ctx, operationID)
		if err != nil {
			return operations.Task{}, err
		}
		for _, task := range operation.Clone().Tasks {
			if task.ID == taskID {
				if task.Status == operations.TaskRunning {
					return operations.Task{}, ErrNotRunning
				}
				return task, nil
			}
		}
		return operations.Task{}, errors.New("unknown task")
	}
	if err != nil {
		return operations.Task{}, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return operations.Task{}, err
		}
		s.mu.Lock()
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return operations.Task{}, err
		}
		var task *operations.Task
		snapshot := s.operation.Clone()
		for i := range snapshot.Tasks {
			if snapshot.Tasks[i].ID == taskID {
				task = &snapshot.Tasks[i]
				break
			}
		}
		changed := s.changed
		finishedRuntime := false
		select {
		case <-r.done:
			finishedRuntime = true
		default:
		}
		s.mu.Unlock()
		if task == nil {
			return operations.Task{}, errors.New("unknown task")
		}
		if task.Status != operations.TaskRunning && task.Status != operations.TaskQueued {
			return *task, nil
		}
		if finishedRuntime {
			return operations.Task{}, ErrNotRunning
		}
		select {
		case <-ctx.Done():
			return operations.Task{}, ctx.Err()
		case <-changed:
		case <-r.done:
			// Recheck the task/error after shutdown has joined its final writer.
		}
	}
}

func eventsAfter(operation operations.Operation, after uint64) ([]operations.Event, error) {
	if after > uint64(len(operation.Events)) {
		return nil, errors.New("event cursor exceeds the latest sequence")
	}
	return append([]operations.Event{}, operation.Events[after:]...), nil
}
