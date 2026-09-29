package runner

import (
	"context"
	"errors"
	"time"

	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

const DefaultQuestionTimeout = 2 * time.Minute

func (r *Runner) Ask(ctx context.Context, command commands.AskQuestionCommand) (operations.Question, error) {
	if err := command.Validate(); err != nil {
		return operations.Question{}, err
	}
	command.Options = append([]string(nil), command.Options...)
	questionID := id.QuestionID()
	return submit(ctx, r, func(ctx context.Context) (operations.Question, error) {
		s, err := r.state(command.OperationID)
		if err != nil {
			return operations.Question{}, err
		}
		if err := active(s); err != nil {
			return operations.Question{}, err
		}
		now := time.Now().UTC()
		timeout := DefaultQuestionTimeout
		if command.TimeoutSeconds > 0 {
			timeout = time.Duration(command.TimeoutSeconds) * time.Second
		}
		question := operations.Question{ID: questionID, Status: operations.QuestionPending, TaskID: command.TaskID, StepID: command.StepID, Text: command.Text, Options: append([]string(nil), command.Options...), AskedAt: now, ExpiresAt: now.Add(timeout)}
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
		s, err := r.state(command.OperationID)
		if err != nil {
			return operations.Operation{}, err
		}
		if err := active(s); err != nil {
			return operations.Operation{}, err
		}
		next, err := r.store.AnswerQuestion(ctx, command.OperationID, command.QuestionID, command.Answer)
		if err := s.acceptUpdate(next, err); err != nil {
			return operations.Operation{}, err
		}
		for _, question := range s.operation.Questions {
			if question.ID == command.QuestionID && question.Status == operations.QuestionExpired {
				return s.operation.Clone(), errors.New("question has expired")
			}
		}
		return s.operation.Clone(), nil
	})
}

func pendingQuestionDeadline(op operations.Operation) bool {
	for _, question := range op.Questions {
		if question.Pending() && !question.ExpiresAt.IsZero() {
			return true
		}
	}
	return false
}

func hasDueQuestions(op operations.Operation, now time.Time) bool {
	for _, question := range op.Questions {
		if question.Pending() && !question.ExpiresAt.IsZero() && !now.Before(question.ExpiresAt) {
			return true
		}
	}
	return false
}

// Sweep before inventory lookup: unavailable agents or an inventory read error
// must not prevent deadlines from releasing operator questions.
func (r *Runner) expireOperatorQuestions() {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	if r.ctx.Err() != nil {
		return
	}
	for _, s := range r.states {
		if s.err != nil {
			continue
		}
		now := time.Now().UTC()
		if hasDueQuestions(s.operation, now) {
			// Retry rejected writes; uncertain writes pause the operation.
			next, err := r.store.ExpireQuestions(ctx, s.operation.ID, now)
			_ = s.acceptUpdate(next, err)
		}
	}
}
