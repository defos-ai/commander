package missionsmcp

import (
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/queries"
)

type taskDetail struct {
	operations.Task
	AgentType           string                `json:"agent_type,omitempty"`
	MissingRequirements []actions.Requirement `json:"missing_requirements,omitempty"`
}

func describeTask(task queries.TaskView) taskDetail {
	return taskDetail{Task: task.Task, AgentType: task.AgentType, MissingRequirements: task.MissingRequirements}
}
