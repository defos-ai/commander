package missionsmcp

import (
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type operationDetail struct {
	operations.Operation
	Tasks              []taskDetail         `json:"tasks"`
	Affinity           *operations.Affinity `json:"affinity,omitempty"`
	LastQuestion       *operations.Question `json:"last_question"`
	RemainingQuestions int                  `json:"remaining_questions"`
}

func describeOperation(operation operations.Operation, records map[string]agents.Agent, now time.Time) operationDetail {
	progress := operation.AssessProgress(now)
	tasks := make([]taskDetail, len(operation.Tasks))
	for i, task := range operation.Tasks {
		var record agents.Agent
		if !operation.Status.Terminal() && task.OperationID == operation.ID {
			record = records[task.AgentID]
		}
		tasks[i] = describeTask(task, record, now)
	}
	return operationDetail{Operation: operation, Tasks: tasks, Affinity: operation.AssessAffinity(), LastQuestion: progress.LastQuestion, RemainingQuestions: progress.RemainingQuestions}
}
