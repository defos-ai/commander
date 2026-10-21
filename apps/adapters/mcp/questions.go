package missionsmcp

import (
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/queries"
)

type operationDetail struct {
	operations.Operation
	Tasks              []taskDetail         `json:"tasks"`
	Affinity           *operations.Affinity `json:"affinity,omitempty"`
	LastQuestion       *operations.Question `json:"last_question"`
	RemainingQuestions int                  `json:"remaining_questions"`
}

func describeOperation(operation queries.OperationView) operationDetail {
	tasks := make([]taskDetail, len(operation.Tasks))
	for i, task := range operation.Tasks {
		tasks[i] = describeTask(task)
	}
	return operationDetail{Operation: operation.Operation, Tasks: tasks, Affinity: operation.Affinity,
		LastQuestion: operation.Progress.LastQuestion, RemainingQuestions: operation.Progress.RemainingQuestions}
}
