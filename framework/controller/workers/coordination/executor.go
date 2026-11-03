package coordination

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/actions"
)

// Executor binds durable dispatch to the same session publisher used for preparation.
type Executor struct {
	*Worker
	Sessions SessionPublisher
}

func (e Executor) DispatchAction(ctx context.Context, agentID string, call actions.ActionCall) (actions.ActionOutput, error) {
	return e.Worker.DispatchAction(ctx, agentID, call, e.Sessions)
}
