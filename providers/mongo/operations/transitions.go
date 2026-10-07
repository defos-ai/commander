package operations

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func (s *OperationMongoStore) RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(operation *operations.Operation) (bool, error) {
		return operation.RefreshPreflight(records)
	})
}

func (s *OperationMongoStore) Recover(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(operation *operations.Operation) (bool, error) {
		return operation.Recover(now)
	})
}

func (s *OperationMongoStore) QueueTask(ctx context.Context, operationID string, task operations.Task) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(next *operations.Operation) (bool, error) {
		return next.QueueTask(task)
	})
}

func (s *OperationMongoStore) DispatchTask(ctx context.Context, operationID string, dispatch operations.TaskDispatch) (operations.Operation, error) {
	return s.update(ctx, operationID, func(next *operations.Operation) error {
		return next.DispatchTask(dispatch)
	})
}

func (s *OperationMongoStore) FinishTask(ctx context.Context, operationID string, result operations.TaskResult) (operations.Operation, error) {
	return s.updateChanged(ctx, operationID, func(next *operations.Operation) (bool, error) {
		return next.FinishTask(result)
	})
}

func (s *OperationMongoStore) ResolveContinuation(ctx context.Context, operationID, taskID string, version uint64, next []actions.NextAction) (operations.Operation, error) {
	return s.update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.ResolveContinuation(taskID, version, next)
	})
}

func (s *OperationMongoStore) AskQuestion(ctx context.Context, operationID string, question operations.Question, version uint64) (operations.Operation, error) {
	return s.update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.AskQuestion(question, version)
	})
}

func (s *OperationMongoStore) AnswerQuestion(ctx context.Context, operationID, questionID, answer string) (operations.Operation, error) {
	return s.update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.AnswerQuestion(questionID, answer)
	})
}

func (s *OperationMongoStore) ExpireQuestions(ctx context.Context, operationID string, now time.Time) (operations.Operation, error) {
	if now.IsZero() {
		return operations.Operation{}, errors.New("question expiration requires a cutoff time")
	}
	return s.updateChanged(ctx, operationID, func(operation *operations.Operation) (bool, error) {
		return operation.ExpireQuestions(now), nil
	})
}

func (s *OperationMongoStore) Complete(ctx context.Context, operationID, reportID string, output json.RawMessage) (operations.Operation, error) {
	return s.update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.Complete(reportID, output)
	})
}

func (s *OperationMongoStore) Fail(ctx context.Context, operationID, reason string) (operations.Operation, error) {
	return s.update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.Fail(reason)
	})
}

func (s *OperationMongoStore) Cancel(ctx context.Context, operationID string) (operations.Operation, error) {
	return s.update(ctx, operationID, func(operation *operations.Operation) error {
		return operation.Cancel()
	})
}

func (s *OperationMongoStore) updateChanged(ctx context.Context, operationID string, mutate func(*operations.Operation) (bool, error)) (operations.Operation, error) {
	return s.update(ctx, operationID, func(next *operations.Operation) error {
		_, err := mutate(next)
		return err
	})
}
