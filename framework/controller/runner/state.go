package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"commander/framework/id"
)

func (r *Runner) change(ctx context.Context, operationID string, mutate func(*operations.Operation) error) (operations.Operation, error) {
	if err := ctx.Err(); err != nil {
		return operations.Operation{}, err
	}
	if r.ctx.Err() != nil {
		return operations.Operation{}, ErrClosed
	}
	ctx, cancel := r.requestContext(ctx)
	defer cancel()
	s, err := r.state(operationID)
	if err != nil {
		return operations.Operation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := active(s); err != nil {
		return operations.Operation{}, err
	}
	next := s.operation.Clone()
	if err := mutate(&next); err != nil {
		return operations.Operation{}, err
	}
	if err := r.save(ctx, s, next); err != nil {
		return operations.Operation{}, err
	}
	return s.operation.Clone(), nil
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

func (r *Runner) save(ctx context.Context, s *operationState, next operations.Operation) error {
	next.Version, next.UpdatedAt = s.operation.Version+1, time.Now().UTC()
	if next.Status == operations.Completed && s.operation.Status != operations.Completed {
		report := reports.Report{ID: id.ReportID(), OperationID: next.ID, Mission: next.Mission.Name,
			Output: append(json.RawMessage(nil), next.Output...), CreatedAt: next.UpdatedAt}
		if err := r.reports.Put(ctx, report); err != nil {
			return fmt.Errorf("save operation report: %w", err)
		}
		next.ReportID = report.ID
		// Publish content before its reference. An operation-store failure may
		// leave an unreferenced report; immutable IDs keep retries independent.
	}
	if err := r.store.Update(ctx, next, s.operation.Version); err != nil {
		return err
	}
	s.operation = next
	close(s.changed)
	s.changed = make(chan struct{})
	return nil
}

func (r *Runner) state(operationID string) (*operationState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	for _, step := range operation.Mission.Steps {
		if step.ID == stepID {
			return nil
		}
	}
	return fmt.Errorf("unknown mission step %q", stepID)
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
	if operation.Status.Terminal() {
		return
	}
	operation.Status = operations.Planning
	for _, task := range operation.Tasks {
		if task.Status == operations.TaskRunning {
			operation.Status = operations.Running
			return
		}
	}
	if operation.Preflight != nil && !operation.Preflight.Ready {
		operation.Status = operations.Waiting
		return
	}
	for _, question := range operation.Questions {
		if question.Pending() {
			operation.Status = operations.Waiting
			return
		}
	}
	if runningTasks(*operation) {
		operation.Status = operations.Running
	}
}

func appendEvent(operation *operations.Operation, kind operations.EventType, taskID, questionID, message string) {
	operation.Events = append(operation.Events, operations.Event{Sequence: uint64(len(operation.Events) + 1),
		OperationID: operation.ID, Type: kind, Status: operation.Status, TaskID: taskID,
		QuestionID: questionID, Message: message, CreatedAt: time.Now().UTC()})
}
