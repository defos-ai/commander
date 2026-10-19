package missionsmcp

import (
	"context"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type taskDetail struct {
	operations.Task
	AgentType           string                `json:"agent_type,omitempty"`
	MissingRequirements []actions.Requirement `json:"missing_requirements,omitempty"`
}

func describeTask(task operations.Task, record agents.Agent, now time.Time) taskDetail {
	detail := taskDetail{Task: task}
	if requirements := operations.AssessTaskRequirements(task, record, now); len(requirements) != 0 {
		detail.AgentType = string(record.Type)
		detail.MissingRequirements = requirements
	}
	return detail
}

// Saved results remain readable when the optional inventory observation fails.
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

func readOperationTaskAgents(ctx context.Context, config Config, operationID string) (map[string]agents.Agent, error) {
	operation, err := config.OperationStore.Get(ctx, operationID)
	if err != nil {
		return nil, ctx.Err()
	}
	if operation.ID != operationID || operation.Status.Terminal() {
		return nil, nil
	}
	return readTaskAgents(ctx, config.AgentStore)
}
