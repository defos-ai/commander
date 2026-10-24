package runner

import (
	"context"
	"errors"
	"reflect"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

func (r *Runner) refreshPreflight(ctx context.Context, state *operationState, records []agents.Agent) error {
	var scopes []sessions.Scope
	if r.sessions != nil {
		if err := r.reconcileSessions(ctx, state); err != nil {
			return err
		}
		var err error
		scopes, err = r.sessions.ScopeRecords(ctx)
		if err != nil {
			return err
		}
	}
	preflight, err := state.operation.AssessPreflight(records, scopes)
	if err != nil {
		return err
	}
	if previous := state.operation.Preflight; previous != nil {
		if preflight.Ready == previous.Ready && reflect.DeepEqual(preflight.Actions, previous.Actions) && reflect.DeepEqual(preflight.Tasks, previous.Tasks) {
			return nil
		}
	}
	var next operations.Operation
	if r.sessions != nil {
		store, ok := r.store.(interface {
			RefreshSessionPreflight(context.Context, string, []agents.Agent, []sessions.Scope) (operations.Operation, error)
		})
		if !ok {
			return errors.New("operation store must support session preflight")
		}
		next, err = store.RefreshSessionPreflight(ctx, state.operation.ID, records, scopes)
	} else {
		next, err = r.store.RefreshPreflight(ctx, state.operation.ID, records)
	}
	return state.acceptUpdate(next, err)
}
