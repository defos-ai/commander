package session

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

// Normalize uses mission contracts and the durable catalog only at acceptance.
// Once saved, task intent is never reinterpreted from a later registration.
func (w *Worker) Normalize(ctx context.Context, next []actions.NextAction, records []agents.Agent, definitions []actions.Definition) ([]actions.NextAction, error) {
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
	if !needsCatalog {
		return runtimeSessions.Normalize(next, records, definitions...), nil
	}
	catalog, err := w.actions.List(ctx)
	if err != nil {
		return nil, err
	}
	fallback := append([]actions.Definition(nil), definitions...)
	for _, definition := range catalog {
		// Live announcements and explicit mission contracts supersede an older
		// last-writer catalog entry. Conflicting live contracts still block.
		if !known[definition.Name] {
			fallback = append(fallback, definition.Definition())
		}
	}
	return runtimeSessions.Normalize(next, records, fallback...), nil
}

func creation(task operations.Task) model.ExecutionRef {
	return model.ExecutionRef{OperationID: task.OperationID, TaskID: task.ID, CommandID: task.CommandID}
}
func (w *Worker) admit(ctx context.Context, task operations.Task) error {
	intent := task.SessionIntent
	if intent == nil {
		return nil
	}
	if err := intent.Validate(); err != nil {
		return err
	}
	if intent.Mode != model.Create {
		return nil
	}
	if w.admitted[task.ID] {
		return nil
	}
	_, err := w.apply(ctx, model.Change{Kind: model.Reserve, ScopeID: intent.ScopeID, ID: intent.ID, Creation: creation(task)})
	if err == nil {
		w.admitted[task.ID] = true
	}
	return err
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
	if task.SessionIntent.Mode == model.Create {
		for _, scope := range scopes {
			if scope.ID == task.SessionIntent.ScopeID && scope.Current != "" && scope.Current != task.SessionIntent.ID {
				return model.ErrScopeReserved
			}
		}
		return nil
	}

	for _, scope := range scopes {
		if scope.ID == task.SessionIntent.ScopeID {
			record, err := scope.Find(task.SessionIntent.ID)
			if err != nil {
				return err
			}
			return record.Available()
		}
	}
	return model.ErrUnknownSession
}

func (w *Worker) ScopeRecords(ctx context.Context) ([]model.Scope, error) {
	return w.store.List(ctx)
}
