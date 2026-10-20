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
	question, remaining := pendingQuestions(operation, now)
	tasks := make([]taskDetail, len(operation.Tasks))
	for i, task := range operation.Tasks {
		var record agents.Agent
		if !operation.Status.Terminal() && task.OperationID == operation.ID {
			record = records[task.AgentID]
		}
		tasks[i] = describeTask(task, record, now)
	}
	return operationDetail{Operation: operation, Tasks: tasks, Affinity: operation.AssessAffinity(), LastQuestion: question, RemainingQuestions: remaining}
}

// Remaining excludes the displayed question. A passed deadline also hides the
// question while the controller is down or has not yet persisted its expiry.
func pendingQuestions(operation operations.Operation, now time.Time) (*operations.Question, int) {
	if operation.Status.Terminal() {
		return nil, 0
	}
	var latest *operations.Question
	count := 0
	for i := range operation.Questions {
		question := &operation.Questions[i]
		if !question.Pending() || !question.ExpiresAt.IsZero() && !now.Before(question.ExpiresAt) {
			continue
		}
		count++
		if latest == nil || question.AskedAt.After(latest.AskedAt) || question.AskedAt.Equal(latest.AskedAt) && question.ID > latest.ID {
			latest = question
		}
	}
	return latest, max(0, count-1)
}
