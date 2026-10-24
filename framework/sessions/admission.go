package sessions

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

// Normalize resolves proposals before the operation transition creates children.
// An invalid automatic proposal remains visible and blocked; it cannot roll back
// its parent's accepted execution result. Explicit callers reject Error instead.
func Normalize(next []actions.NextAction, records []agents.Agent, definitions ...actions.Definition) []actions.NextAction {
	result := actions.CloneNextActions(next)
	for i := range result {
		a := &result[i]
		a.SessionIntent = nil
		mode := ""
		seen := false
		conflict := false
		for _, record := range records {
			for _, definition := range record.Actions {
				if definition.Name == a.ActionName {
					if seen && mode != definition.SessionMode {
						conflict = true
					}
					mode = definition.SessionMode
					seen = true
				}
			}
		}
		// A saved mission contract or learned catalog entry can resolve intent
		// before an executor returns. It supplies no readiness evidence.
		for _, definition := range definitions {
			if definition.Name != a.ActionName {
				continue
			}
			if seen && mode != definition.SessionMode {
				conflict = true
			}
			mode, seen = definition.SessionMode, true
		}
		var err error
		if !seen {
			err = errors.New("action_contract_unavailable: assign fresh work after the action is registered")
		} else if conflict {
			err = errors.New("conflicting session action contracts")
		} else {
			a.SessionIntent, err = model.ResolveIntent(mode, a.Input)
		}
		if err != nil {
			a.SessionIntent = &model.Intent{Error: err.Error()}
		}
	}
	return result
}

// Normalize uses mission contracts and the durable catalog only at acceptance.
// Once saved, task intent is never reinterpreted from a later registration.
func (c *Coordinator) Normalize(ctx context.Context, next []actions.NextAction, records []agents.Agent, definitions []actions.Definition) ([]actions.NextAction, error) {
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
		return Normalize(next, records, definitions...), nil
	}
	catalog, err := c.actions.List(ctx)
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
	return Normalize(next, records, fallback...), nil
}

func creation(task operations.Task) model.ExecutionRef {
	return model.ExecutionRef{OperationID: task.OperationID, TaskID: task.ID, CommandID: task.CommandID}
}
func (c *Coordinator) admit(ctx context.Context, task operations.Task) error {
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
	if c.admitted[task.ID] {
		return nil
	}
	_, err := c.apply(ctx, model.Change{Kind: model.Reserve, ScopeID: intent.ScopeID, ID: intent.ID, Creation: creation(task)})
	if err == nil {
		c.admitted[task.ID] = true
	}
	return err
}

func (c *Coordinator) CheckAdmission(ctx context.Context, task operations.Task) error {
	if task.SessionIntent == nil {
		return nil
	}
	if err := task.SessionIntent.Validate(); err != nil {
		return err
	}
	scopes, err := c.store.List(ctx)
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

// ScopeRecords supplies independent snapshots for a single shared placement assessment.
func (c *Coordinator) ScopeRecords(ctx context.Context) ([]model.Scope, error) {
	return c.store.List(ctx)
}
