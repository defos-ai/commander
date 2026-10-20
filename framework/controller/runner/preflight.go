package runner

import (
	"context"
	"reflect"

	"github.com/defos-ai/commander/framework/domain/agents"
)

func (r *Runner) refreshPreflight(ctx context.Context, state *operationState, records []agents.Agent) error {
	preflight, err := state.operation.AssessPreflight(records)
	if err != nil {
		return err
	}
	if previous := state.operation.Preflight; previous != nil {
		if preflight.Ready == previous.Ready && reflect.DeepEqual(preflight.Actions, previous.Actions) {
			return nil
		}
	}
	next, err := r.store.RefreshPreflight(ctx, state.operation.ID, records)
	return state.acceptUpdate(next, err)
}
