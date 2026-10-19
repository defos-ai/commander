package operations

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

// Live inventory enriches saved queries; losing that optional observation must
// not make persisted task results unavailable.
func readTaskAgents(ctx context.Context, store agents.AgentStore) (map[string]agents.Agent, error) {
	records, err := store.List(ctx)
	if err != nil {
		return nil, ctx.Err()
	}
	byID := make(map[string]agents.Agent, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	return byID, nil
}

func readOperationTaskAgents(ctx context.Context, store operations.OperationStore, agentStore agents.AgentStore, operationID string) (map[string]agents.Agent, error) {
	operation, err := store.Get(ctx, operationID)
	if err != nil {
		return nil, ctx.Err()
	}
	if operation.ID != operationID || operation.Status.Terminal() {
		return nil, nil
	}
	return readTaskAgents(ctx, agentStore)
}
