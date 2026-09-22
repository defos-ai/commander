package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"commander/framework/id"
)

func (r *Runner) Missions(ctx context.Context) ([]missions.Mission, error) {
	return r.missions.List(ctx)
}

// Start validates and records an operation. It never requires all required actions
// to exist and does not invent an action-selection policy. The AI can assign work
// after inspecting the registered agents, results and suggested mission steps.
func (r *Runner) Start(ctx context.Context, command commands.StartOperationCommand) (operations.Operation, error) {
	if err := ctx.Err(); err != nil {
		return operations.Operation{}, err
	}
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	if r.ctx.Err() != nil {
		return operations.Operation{}, ErrClosed
	}
	ctx, cancel := r.requestContext(ctx)
	defer cancel()
	definition, err := r.missions.Get(ctx, command.MissionName)
	if err != nil {
		return operations.Operation{}, err
	}
	definition = definition.Clone()
	if err := definition.Validate(); err != nil {
		return operations.Operation{}, err
	}
	input := append(json.RawMessage(nil), command.Input...)
	if err := definition.ValidateInput(input); err != nil {
		return operations.Operation{}, err
	}
	now := time.Now().UTC()
	operationID := command.OperationID
	if operationID == "" {
		operationID = id.OperationID()
	}
	operation := operations.Operation{ID: operationID, Mission: definition.Clone(), Input: input,
		Status: operations.Planning, Tasks: []operations.Task{}, Questions: []operations.Question{},
		Version: 1, CreatedAt: now, UpdatedAt: now}
	appendEvent(&operation, operations.OperationStarted, "", "", "Awaiting the next action from the decision maker")
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return operations.Operation{}, ErrClosed
	}
	if err := r.store.Create(ctx, operation); err != nil {
		return operations.Operation{}, err
	}
	r.states[operation.ID] = &operationState{operation: operation, changed: make(chan struct{})}
	return operation.Clone(), nil
}

// Get and List remain readable after shutdown. Stores may outlive the runner.
func (r *Runner) Get(ctx context.Context, operationID string) (operations.Operation, error) {
	return r.store.Get(ctx, operationID)
}

func (r *Runner) List(ctx context.Context) ([]operations.Operation, error) { return r.store.List(ctx) }

// GetReport reads the report referenced by a completed operation, including
// after restart. Older operations without report_id retain their inline Output.
func (r *Runner) GetReport(ctx context.Context, operationID string) (reports.Report, error) {
	operation, err := r.store.Get(ctx, operationID)
	if err != nil {
		return reports.Report{}, err
	}
	if operation.Status != operations.Completed || operation.ReportID == "" {
		return reports.Report{}, fmt.Errorf("operation has no stored report: %w", reports.ErrUnknownReport)
	}
	report, err := r.reports.Get(ctx, operation.ReportID)
	if err != nil {
		return reports.Report{}, err
	}
	if report.OperationID != operation.ID || report.Mission != operation.Mission.Name {
		return reports.Report{}, errors.New("stored report does not belong to the operation")
	}
	return report, nil
}

func (r *Runner) Complete(ctx context.Context, command commands.CompleteOperationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	output := command.Output
	return r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		if runningTasks(*next) {
			return errors.New("operation still has active tasks")
		}
		for _, question := range next.Questions {
			if question.Pending() {
				return ErrQuestionPending
			}
		}
		if err := next.Mission.ValidateOutput(next.Input, output); err != nil {
			return err
		}
		// Explicit completion is the model's decision to stop all remaining branches.
		for i := range next.Tasks {
			if c := next.Tasks[i].Continuation; c != nil && c.Status != operations.ContinuationResolved {
				if c.DecisionBy != operations.DecisionModel {
					return errors.New("operation still has unresolved automatic or operator work")
				}
				c.Status = operations.ContinuationResolved
				c.Version++
			}
		}
		next.Output, next.Status = append(json.RawMessage(nil), output...), operations.Completed
		appendEvent(next, operations.OperationCompleted, "", "", "Operation completed")
		return nil
	})
}

func (r *Runner) Fail(ctx context.Context, command commands.FailOperationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	reason := command.Reason
	return r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		next.Status, next.Error = operations.Failed, reason
		cancelQueued(next)
		appendEvent(next, operations.OperationFailed, "", "", reason)
		return nil
	})
}

// Cancel stops new assignments. Already-published commands may still execute;
// their replies are collected until the task timeout, preserving partial results.
func (r *Runner) Cancel(ctx context.Context, command commands.CancelOperationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	return r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		next.Status = operations.Cancelled
		cancelQueued(next)
		appendEvent(next, operations.OperationCancelled, "", "", "New assignments cancelled; dispatched tasks may still finish")
		return nil
	})
}
