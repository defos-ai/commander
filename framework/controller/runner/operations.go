package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	"commander/framework/id"
)

func (r *Runner) Missions(ctx context.Context) ([]missions.Mission, error) {
	return r.missions.List(ctx)
}

// Start saves unavailable requirements so the decision maker can inspect them.
// Tasks stay queued until preflight passes; the operation itself is still created.
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
		return operations.Operation{}, fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
	}
	now := time.Now().UTC()
	operationID := command.OperationID
	if operationID == "" {
		operationID = id.OperationID()
	}
	operation := operations.Operation{ID: operationID, Mission: definition.Clone(), Input: input,
		Status: operations.Planning, Tasks: []operations.Task{}, Questions: []operations.Question{},
		CreatedAt: now, UpdatedAt: now}
	records, err := r.catalog.Agents(ctx)
	if err != nil {
		return operations.Operation{}, fmt.Errorf("operation preflight: %w", err)
	}
	operation.Preflight = operations.AssessPreflight(definition, records)
	operation.RefreshStatus()
	operation.AppendEvent(operations.OperationStarted, "", "", "Awaiting the next action from the decision maker")
	return submit(ctx, r, func(ctx context.Context) (operations.Operation, error) {
		if err := r.store.Create(ctx, operation); err != nil {
			if errors.Is(err, operations.ErrWriteUncertain) {
				err = fmt.Errorf("restart controller to reconcile operation %s: %w", operation.ID, err)
				r.states[operation.ID] = &operationState{operation: operation, changed: make(chan struct{}), err: err}
				return operation.Clone(), err
			}
			return operations.Operation{}, err
		}
		r.states[operation.ID] = &operationState{operation: operation, changed: make(chan struct{})}
		return operation.Clone(), nil
	})
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
	output := append(json.RawMessage(nil), command.Output...)
	return submit(ctx, r, func(ctx context.Context) (operations.Operation, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Operation{}, err
		}
		next, err := r.store.Get(ctx, command.OperationID)
		if err != nil {
			return operations.Operation{}, err
		}
		if err := next.ValidateCompletion(output); err != nil {
			return operations.Operation{}, err
		}
		report := reports.Report{ID: id.ReportID(), OperationID: next.ID, Mission: next.Mission.Name,
			Output: append(json.RawMessage(nil), output...), CreatedAt: time.Now().UTC()}
		// Publish immutable content before its reference. A failed operation update
		// can leave an unreferenced report, but never a reference to missing content.
		if err := r.reports.Put(ctx, report); err != nil {
			return operations.Operation{}, fmt.Errorf("save operation report: %w", err)
		}
		next, err = r.store.Complete(ctx, command.OperationID, report.ID, output)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Operation{}, err
		}
		return s.operation.Clone(), nil
	})
}

func (r *Runner) Fail(ctx context.Context, command commands.FailOperationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	return submit(ctx, r, func(ctx context.Context) (operations.Operation, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Operation{}, err
		}
		next, err := r.store.Fail(ctx, command.OperationID, command.Reason)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Operation{}, err
		}
		return s.operation.Clone(), nil
	})
}

// Cancel stops new assignments. Already-published commands may still execute;
// their replies are collected until the task timeout, preserving partial results.
func (r *Runner) Cancel(ctx context.Context, command commands.CancelOperationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	return submit(ctx, r, func(ctx context.Context) (operations.Operation, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Operation{}, err
		}
		next, err := r.store.Cancel(ctx, command.OperationID)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Operation{}, err
		}
		return s.operation.Clone(), nil
	})
}
