package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"commander/framework/domain/operations"
)

func (r *Runner) change(ctx context.Context, operationID string, mutate func(*operations.Operation) error) (operations.Operation, error) {
	return submit(ctx, r, func(ctx context.Context) (operations.Operation, error) {
		s, err := r.state(operationID)
		if err != nil {
			return operations.Operation{}, err
		}
		if err := active(s); err != nil {
			return operations.Operation{}, err
		}
		if err := r.update(ctx, s, func(next *operations.Operation) error {
			if next.Status.Terminal() {
				return ErrFinished
			}
			return mutate(next)
		}); err != nil {
			return operations.Operation{}, err
		}
		return s.operation.Clone(), nil
	})
}

// requestContext lets shutdown interrupt request-owned store operations, even
// when their caller used a background context. Final shutdown/result persistence
// deliberately uses separate bounded contexts and does not call this helper.
func (r *Runner) requestContext(ctx context.Context) (context.Context, func()) {
	request, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel)
	if r.ctx.Err() != nil {
		cancel()
	}
	return request, func() {
		stop()
		cancel()
	}
}

func (r *Runner) update(ctx context.Context, s *operationState, mutate func(*operations.Operation) error) error {
	next, err := r.store.Update(ctx, s.operation.ID, func(next *operations.Operation) error {
		if err := mutate(next); err != nil {
			return err
		}
		next.UpdatedAt = time.Now().UTC()
		return nil
	})
	return s.acceptUpdate(next, err)
}

func (s *operationState) acceptUpdate(next operations.Operation, err error) error {
	if err != nil && !errors.Is(err, operations.ErrWriteUncertain) {
		return err
	}
	s.operation = next
	if err != nil {
		err = fmt.Errorf("restart controller to reconcile operation %s: %w", next.ID, err)
		s.err = errors.Join(s.err, err)
	}
	close(s.changed)
	s.changed = make(chan struct{})
	return err
}

func (r *Runner) state(operationID string) (*operationState, error) {
	s, exists := r.states[operationID]
	if !exists {
		return nil, operations.ErrUnknownOperation
	}
	return s, nil
}

func active(s *operationState) error {
	if s.err != nil {
		return s.err
	}
	if s.operation.Status.Terminal() {
		return ErrFinished
	}
	return nil
}

func validStep(operation operations.Operation, stepID string) error {
	if stepID == "" {
		return nil
	}
	if len(operation.Mission.Steps) == 0 {
		return fmt.Errorf("unknown mission step %q: mission %q declares no steps; omit step_id and use action_name when assigning a task", stepID, operation.Mission.Name)
	}
	for _, step := range operation.Mission.Steps {
		if step.ID == stepID {
			return nil
		}
	}
	return fmt.Errorf("unknown mission step %q for mission %q; use a step ID declared in mission.steps", stepID, operation.Mission.Name)
}

func runningTasks(operation operations.Operation) bool {
	for _, task := range operation.Tasks {
		if task.Status == operations.TaskRunning || task.Status == operations.TaskQueued {
			return true
		}
	}
	return false
}

func refreshStatus(operation *operations.Operation) {
	operation.RefreshStatus()
}

func appendEvent(operation *operations.Operation, kind operations.EventType, taskID, questionID, message string) {
	operation.AppendEvent(kind, taskID, questionID, message)
}
