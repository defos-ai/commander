package coordination

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/actions"
)

// Executor uses one session coordinator for preparation and publication.
type Executor struct {
	*Worker
	Sessions SessionDispatcher
}

func (e Executor) RunAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return e.Worker.runAction(ctx, agentID, call, e.Sessions)
}

func (e Executor) DispatchAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return e.Worker.DispatchAction(ctx, agentID, call, e.Sessions)
}
