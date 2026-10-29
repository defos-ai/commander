package runner

import (
	"context"
	"errors"
	"time"

	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

const DefaultQuestionTimeout = operations.DefaultQuestionTimeout

func (r *Runner) Ask(ctx context.Context, command commands.AskQuestionCommand) (operations.Question, error) {
	if err := command.Validate(); err != nil {
		return operations.Question{}, err
	}
	command.Options = append([]string(nil), command.Options...)
	questionID := id.QuestionID()
	return submit(ctx, r, func(ctx context.Context) (operations.Question, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Question{}, err
		}
		now := time.Now().UTC()
		timeout := DefaultQuestionTimeout
		if command.TimeoutSeconds > 0 {
			timeout = time.Duration(command.TimeoutSeconds) * time.Second
		}
		question := operations.Question{ID: questionID, Status: operations.QuestionPending, TaskID: command.TaskID, Text: command.Text, Options: append([]string(nil), command.Options...), AskedAt: now, FallbackAt: now.Add(timeout)}
		next, err := r.store.AskQuestion(ctx, command.OperationID, question, command.Version)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Question{}, err
		}
		for _, saved := range s.operation.Clone().Questions {
			if saved.ID == questionID {
				return saved, nil
			}
		}
		return operations.Question{}, errors.New("saved question is missing")
	})
}

func (r *Runner) Answer(ctx context.Context, command commands.AnswerQuestionCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	return submit(ctx, r, func(ctx context.Context) (operations.Operation, error) {
		s, err := r.activeState(ctx, command.OperationID)
		if err != nil {
			return operations.Operation{}, err
		}
		next, err := r.store.AnswerQuestion(ctx, command.OperationID, command.QuestionID, command.Answer, command.AnsweredBy)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Operation{}, err
		}
		return s.operation.Clone(), nil
	})
}
