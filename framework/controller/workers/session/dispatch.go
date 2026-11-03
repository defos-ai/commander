package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

func (w *Worker) Prepare(ctx context.Context, a runtimeSessions.Assignment) (runtimeSessions.PreparedDispatch, error) {
	if err := w.lock(ctx); err != nil {
		return runtimeSessions.PreparedDispatch{}, err
	}
	defer w.unlock()
	op, err := w.operations.Get(ctx, a.OperationID)
	if err != nil {
		return runtimeSessions.PreparedDispatch{}, err
	}
	task, err := w.operations.GetTask(ctx, a.OperationID, a.TaskID)
	if err != nil {
		return runtimeSessions.PreparedDispatch{}, err
	}
	if err := w.admit(ctx, task); err != nil {
		return runtimeSessions.PreparedDispatch{}, err
	}
	scopes, err := w.store.List(ctx)
	if err != nil {
		return runtimeSessions.PreparedDispatch{}, err
	}
	records, err := w.agents.List(ctx)
	if err != nil {
		return runtimeSessions.PreparedDispatch{}, err
	}
	for i := range records {
		records[i] = records[i].Snapshot(time.Now().UTC())
	}
	placement := op.SessionPlacement(task, records, scopes)
	if placement.Reason != "" {
		return runtimeSessions.PreparedDispatch{}, errors.New(placement.Reason)
	}
	if task.AgentID != "" {
		a.Candidate = task.AgentID
	}
	if placement.OwnerAgentID != "" && placement.OwnerAgentID != a.Candidate {
		return runtimeSessions.PreparedDispatch{}, fmt.Errorf("placement_conflict: requires %s", placement.OwnerAgentID)
	}
	available := false
	for _, record := range records {
		if record.ID == a.Candidate && !record.Stale {
			for _, definition := range record.Actions {
				if definition.Name == task.ActionName && operations.AssessAgentAction(record, definition).Reason == "" {
					available = task.MatchesSessionMode(definition.SessionMode)
				}
			}
		}
	}
	if !available {
		return runtimeSessions.PreparedDispatch{}, errors.New("owner_unavailable")
	}
	if task.Status == operations.TaskQueued {
		started := time.Now().UTC()
		if a.Timeout <= 0 {
			a.Timeout = operations.DefaultTaskTimeout
		}
		op, err = w.operations.DispatchTask(ctx, op.ID, operations.TaskDispatch{TaskID: task.ID, AgentID: a.Candidate, StartedAt: started, DeadlineAt: started.Add(a.Timeout)})
		if err != nil {
			return runtimeSessions.PreparedDispatch{Operation: op, Task: task}, err
		}
		task, err = w.operations.GetTask(ctx, op.ID, task.ID)
		if err != nil {
			return runtimeSessions.PreparedDispatch{Operation: op}, errors.Join(operations.ErrWriteUncertain, err)
		}
	}
	if op.Status.Terminal() || task.Status != operations.TaskRunning || task.DeadlineAt == nil || !time.Now().Before(*task.DeadlineAt) {
		return runtimeSessions.PreparedDispatch{}, operations.ErrOperationConflict
	}
	// Dispatch is now durable. Any later failure requires reconciliation, even
	// if the failing store reports a definite rejection. Never leave the runner
	// believing this task is still queued or allow selection of another owner.
	uncertain := func(err error) (runtimeSessions.PreparedDispatch, error) {
		return runtimeSessions.PreparedDispatch{Operation: op, Task: task}, errors.Join(operations.ErrWriteUncertain, err)
	}
	var binding *model.Binding
	if intent := task.SessionIntent; intent != nil {
		var record model.Record
		if intent.Mode == model.Create {
			record, err = w.apply(ctx, model.Change{Kind: model.Bind, ScopeID: intent.ScopeID, ID: intent.ID, OwnerAgentID: task.AgentID, Creation: creation(task)})
		} else {
			var scope model.Scope
			scope, err = w.store.Get(ctx, intent.ScopeID)
			if err == nil {
				record, err = scope.Find(intent.ID)
			}
		}
		if err != nil {
			return uncertain(err)
		}
		if err := record.Available(); err != nil {
			return uncertain(err)
		}
		if record.OwnerAgentID != task.AgentID {
			return uncertain(model.ErrConflict)
		}
		b := record.Binding(intent.Mode)
		binding = &b
	}
	call := actions.ActionCall{ActionName: task.ActionName, Input: task.Input, Work: messages.WorkContext{OperationID: task.OperationID, TaskID: task.ID}, Session: binding}
	command, err := commands.NewRunAction(task.AgentID, call)
	if err != nil {
		return uncertain(err)
	}
	command.ExpiresAt = *task.DeadlineAt
	claimed, _, err := w.executions.Claim(ctx, command)
	if err != nil {
		return uncertain(err)
	}
	return runtimeSessions.PreparedDispatch{Operation: op, Task: task, Command: claimed.Command, Binding: binding}, nil
}

// Publish serializes the final binding check with close/abandon. The callback is
// a bounded broker publication, never an action/owner reply wait. Only a saved
// execution envelope can cross this seam; new claims belong to Prepare.
func (w *Worker) Publish(ctx context.Context, command messages.Message, publish func(context.Context, messages.Message) error) error {
	if err := w.lock(ctx); err != nil {
		return err
	}
	defer w.unlock()
	stored, err := w.executions.Get(ctx, command.AgentID, command.ID)
	if err != nil {
		return err
	}
	var call actions.ActionCall
	if err := json.Unmarshal(stored.Command.Payload, &call); err != nil {
		return err
	}
	if call.Session != nil {
		scope, err := w.store.Get(ctx, call.Session.ScopeID)
		if err != nil {
			return err
		}
		r, err := scope.Find(call.Session.ID)
		if err != nil {
			return err
		}
		if err := r.Available(); err != nil {
			return err
		}
		if r.Binding(call.Session.Mode) != *call.Session {
			return model.ErrConflict
		}
	}
	if stored.Result != nil {
		return nil
	}
	return publish(ctx, stored.Command)
}
