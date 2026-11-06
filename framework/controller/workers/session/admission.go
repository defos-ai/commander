package session

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

// Normalize uses mission contracts and the durable catalog only at acceptance.
// Once saved, task intent is never reinterpreted from a later registration.
func (w *Worker) Normalize(ctx context.Context, op operations.Operation, parent *operations.Task, next []actions.NextAction, records []agents.Agent) ([]actions.NextAction, error) {
	definitions := op.Mission.ActionDefinitions
	known := make(map[string]bool)
	for _, definition := range definitions {
		known[definition.Name] = true
	}
	for _, record := range records {
		for _, definition := range record.Actions {
			known[definition.Name] = true
		}
	}
	needsCatalog := false
	for _, proposal := range next {
		needsCatalog = needsCatalog || !known[proposal.ActionName]
	}
	fallback := append([]actions.Definition(nil), definitions...)
	if needsCatalog {
		catalog, err := w.actions.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, definition := range catalog {
			// Live announcements and mission contracts supersede learned entries.
			if !known[definition.Name] {
				fallback = append(fallback, definition.Definition())
			}
		}
	}
	return runtimeSessions.Normalize(parent, next, records, fallback...)
}

func (w *Worker) admit(ctx context.Context, task operations.Task) (bool, error) {
	intent := task.SessionIntent
	if intent == nil {
		return false, nil
	}
	if err := intent.Validate(); err != nil {
		return false, err
	}
	if intent.Mode != model.Open {
		return false, nil
	}
	if w.admitted[task.ID] {
		return true, nil
	}
	creator, err := w.createsSession(ctx, task)
	if err != nil || !creator {
		return creator, err
	}
	_, err = w.apply(ctx, model.Change{Kind: model.Reserve, ScopeID: intent.ScopeID, ID: intent.ID,
		CommandID: task.CommandID, OperationID: task.OperationID, TaskID: task.ID})
	if err == nil {
		w.admitted[task.ID] = true
	}
	return true, err
}

func (w *Worker) CheckAdmission(ctx context.Context, task operations.Task) error {
	if task.SessionIntent == nil {
		return nil
	}
	if err := task.SessionIntent.Validate(); err != nil {
		return err
	}
	scopes, err := w.store.List(ctx)
	if err != nil {
		return err
	}
	fresh := task.SessionIntent.Mode == model.Open && task.SessionIntent.ID == task.CommandID
	for _, scope := range scopes {
		if scope.ID == task.SessionIntent.ScopeID {
			record, err := scope.Find(task.SessionIntent.ID)
			if errors.Is(err, model.ErrUnknownSession) && fresh {
				if scope.Current != "" {
					return model.ErrScopeReserved
				}
				return nil
			}
			if err != nil {
				return err
			}
			if record.CommandID == task.CommandID && (task.SessionIntent.Mode != model.Open || !record.CreatedBy(task.CommandID, task.OperationID, task.ID)) {
				return model.ErrConflict
			}
			return record.Available()
		}
	}
	if fresh {
		return nil
	}
	return model.ErrUnknownSession
}

func (w *Worker) createsSession(ctx context.Context, task operations.Task) (bool, error) {
	intent := task.SessionIntent
	scope, err := w.store.Get(ctx, intent.ScopeID)
	if errors.Is(err, model.ErrUnknownSession) {
		return intent.ID == task.CommandID, nil
	}
	if err != nil {
		return false, err
	}
	record, err := scope.Find(intent.ID)
	if errors.Is(err, model.ErrUnknownSession) {
		return intent.ID == task.CommandID, nil
	}
	if err != nil {
		return false, err
	}
	if record.CommandID == task.CommandID && !record.CreatedBy(task.CommandID, task.OperationID, task.ID) {
		return false, model.ErrConflict
	}
	return record.CreatedBy(task.CommandID, task.OperationID, task.ID), nil
}

func (w *Worker) ScopeRecords(ctx context.Context) ([]model.Scope, error) {
	return w.store.List(ctx)
}
