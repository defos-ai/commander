package fakes

import (
	"context"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type QuestionAnswers struct {
	commands.Operations
	submitted chan commands.AnswerQuestionCommand
}

func NewQuestionAnswers() *QuestionAnswers {
	return &QuestionAnswers{submitted: make(chan commands.AnswerQuestionCommand, 1)}
}

func (s *QuestionAnswers) Submitted() <-chan commands.AnswerQuestionCommand { return s.submitted }

func (s *QuestionAnswers) Answer(ctx context.Context, command commands.AnswerQuestionCommand) (operations.Operation, error) {
	select {
	case s.submitted <- command:
	case <-ctx.Done():
		return operations.Operation{}, ctx.Err()
	}
	now := time.Now().UTC()
	return operations.Operation{ID: command.OperationID, Status: operations.Planning, Questions: []operations.Question{{
		ID: command.QuestionID, Status: operations.QuestionAnswered, Answer: command.Answer,
		AnsweredBy: command.AnsweredBy, AnsweredAt: &now,
	}}}, nil
}
