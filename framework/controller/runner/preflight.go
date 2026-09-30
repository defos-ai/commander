package runner

import (
	"context"
	"reflect"

	"commander/framework/domain/agents"
	"commander/framework/domain/operations"
)

func (r *Runner) refreshPreflight(ctx context.Context, state *operationState, records []agents.Agent) error {
	if r.catalog == nil {
		return nil
	}
	preflight := operations.AssessPreflight(state.operation.Mission, records)
	if previous := state.operation.Preflight; previous != nil {
		if preflight.Ready == previous.Ready && reflect.DeepEqual(preflight.Actions, previous.Actions) {
			return nil
		}
	}
	next, err := r.store.RefreshPreflight(ctx, state.operation.ID, records)
	return state.acceptUpdate(next, err)
}
