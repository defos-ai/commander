package runner

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/domain/operations"
)

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

func (r *Runner) activeState(ctx context.Context, operationID string) (*operationState, error) {
	s, err := r.state(operationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		// Recovery omits finished operations without outstanding work from memory.
		stored, readErr := r.store.Get(ctx, operationID)
		if readErr != nil {
			return nil, readErr
		}
		if stored.Status.Terminal() {
			return nil, ErrFinished
		}
	}
	if err != nil {
		return nil, err
	}
	if err := active(s); err != nil {
		return nil, err
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

func runningTasks(operation operations.Operation) bool {
	for _, task := range operation.Tasks {
		if task.Status == operations.TaskRunning || task.Status == operations.TaskQueued {
			return true
		}
	}
	return false
}
