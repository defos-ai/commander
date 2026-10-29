package missionsmcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/queries"
)

func questionAnswerError(ctx context.Context, query queries.Operations, request AnswerQuestionRequest, err error) error {
	if !errors.Is(err, operations.ErrQuestionOperatorWaiting) || errors.Is(err, operations.ErrWriteUncertain) {
		return err
	}
	guidance := "Read get_operation before submitting again; the operator can answer in Admin UI's Questions section or Playground's Operator input panel."
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	operation, readErr := query.Get(readCtx, request.OperationID)
	if readErr == nil && !operation.Status.Terminal() {
		for _, question := range operation.Questions {
			if question.ID == request.QuestionID && question.Pending() && !question.FallbackAt.IsZero() {
				return fmt.Errorf("question_operator_waiting: %w; fallback_at=%s. Schedule a fresh get_operation at or after that time before answering. %s", err, question.FallbackAt.UTC().Format(time.RFC3339Nano), guidance)
			}
		}
	}
	return fmt.Errorf("question_operator_waiting: %w. %s", err, guidance)
}

type operationDetail struct {
	operations.Operation
	Progress           operationProgress    `json:"progress"`
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
	return operationDetail{Operation: operation.Operation, Tasks: tasks, Affinity: operation.Affinity, Progress: describeProgress(operation.Progress),
		LastQuestion: operation.Progress.LastQuestion, RemainingQuestions: operation.Progress.RemainingQuestions}
}
