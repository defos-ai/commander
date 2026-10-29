package fixtures

import (
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func Question(taskID string) operations.Question {
	now := time.Now().UTC()
	return operations.Question{ID: id.QuestionID(), TaskID: taskID, Status: operations.QuestionPending,
		Text: "Continue review?", Options: []string{"yes", "no"}, AskedAt: now, FallbackAt: now.Add(time.Hour)}
}
