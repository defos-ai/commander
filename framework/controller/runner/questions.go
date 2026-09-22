package runner

import (
	"context"
	"errors"
	"time"

	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

func (r *Runner) Ask(ctx context.Context, command commands.AskQuestionCommand) (operations.Question, error) {
	if err := command.Validate(); err != nil {
		return operations.Question{}, err
	}
	var question operations.Question
	_, err := r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		if err := validStep(*next, command.StepID); err != nil {
			return err
		}
		question = operations.Question{ID: id.QuestionID(), TaskID: command.TaskID, StepID: command.StepID, Text: command.Text, Options: append([]string(nil), command.Options...), AskedAt: time.Now().UTC()}
		if command.TaskID != "" {
			i := taskIndex(next, command.TaskID)
			if i < 0 {
				return errors.New("unknown task")
			}
			c := next.Tasks[i].Continuation
			if c == nil || c.Version != command.Version || c.Status != operations.ContinuationPending || c.DecisionBy != operations.DecisionModel {
				return errors.New("only a pending model decision can request operator intervention")
			}
			question.StepID = next.Tasks[i].StepID
			c.DecisionBy, c.Status, c.QuestionID = operations.DecisionUser, operations.ContinuationWaiting, question.ID
			c.Version++
		}
		next.Questions = append(next.Questions, question)
		refreshStatus(next)
		appendEvent(next, operations.QuestionAsked, command.TaskID, question.ID, "Operator intervention requested")
		return nil
	})
	return question, err
}

func (r *Runner) Answer(ctx context.Context, command commands.AnswerQuestionCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	return r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		for i := range next.Questions {
			question := &next.Questions[i]
			if question.ID != command.QuestionID {
				continue
			}
			if question.AnsweredAt != nil {
				return errors.New("question is already answered")
			}
			now := time.Now().UTC()
			question.Answer, question.AnsweredAt = command.Answer, &now
			if question.TaskID != "" {
				i := taskIndex(next, question.TaskID)
				if i < 0 {
					return errors.New("question task is missing")
				}
				c := next.Tasks[i].Continuation
				if c == nil || c.QuestionID != question.ID || c.DecisionBy != operations.DecisionUser {
					return errors.New("question does not match the waiting continuation")
				}
				c.DecisionBy, c.Status = operations.DecisionModel, operations.ContinuationPending
				c.Version++
			}
			refreshStatus(next)
			appendEvent(next, operations.QuestionAnswered, "", question.ID, "Answer received; planning can continue")
			return nil
		}
		return errors.New("unknown question")
	})
}
