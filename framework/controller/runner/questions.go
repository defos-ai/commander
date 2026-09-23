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
	result, err := r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		if err := validStep(*next, command.StepID); err != nil {
			return err
		}
		now := time.Now().UTC()
		timeout := DefaultQuestionTimeout
		if command.TimeoutSeconds > 0 {
			timeout = time.Duration(command.TimeoutSeconds) * time.Second
		}
		question := operations.Question{ID: questionID, Status: operations.QuestionPending, TaskID: command.TaskID, StepID: command.StepID, Text: command.Text, Options: append([]string(nil), command.Options...), AskedAt: now, ExpiresAt: now.Add(timeout)}
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
	if err != nil {
		return operations.Question{}, err
	}
	return result.Questions[len(result.Questions)-1], nil
}

func (r *Runner) Answer(ctx context.Context, command commands.AnswerQuestionCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	result, err := r.change(ctx, command.OperationID, func(next *operations.Operation) error {
		for i := range next.Questions {
			question := &next.Questions[i]
			if question.ID != command.QuestionID {
				continue
			}
			switch question.Status {
			case operations.QuestionAnswered:
				return errors.New("question is already answered")
			case operations.QuestionExpired:
				return errors.New("question has expired")
			case operations.QuestionPending:
			default:
				return errors.New("question is not pending")
			}
			now := time.Now().UTC()
			if !question.ExpiresAt.IsZero() && !now.Before(question.ExpiresAt) {
				// Commit expiration even when the caller's answer is rejected.
				expireQuestions(next, now)
				return nil
			}
			question.Status, question.Answer, question.AnsweredAt = operations.QuestionAnswered, command.Answer, &now
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
			appendEvent(next, operations.QuestionAnsweredEvent, "", question.ID, "Answer received; planning can continue")
			return nil
		}
		return errors.New("unknown question")
	})
	if err != nil {
		return operations.Operation{}, err
	}
	for _, question := range result.Questions {
		if question.ID == command.QuestionID && question.Status == operations.QuestionExpired {
			return result, errors.New("question has expired")
		}
	}
	return result, nil
}

// Expiration records an unanswered question and gives a linked decision back
// to the model. It never supplies an answer or resumes a finished operation.
func expireQuestions(op *operations.Operation, now time.Time) bool {
	changed := false
	for i := range op.Questions {
		question := &op.Questions[i]
		if !question.Pending() || question.ExpiresAt.IsZero() || now.Before(question.ExpiresAt) {
			continue
		}
		question.Status = operations.QuestionExpired
		if !op.Status.Terminal() && question.TaskID != "" {
			if j := taskIndex(op, question.TaskID); j >= 0 {
				c := op.Tasks[j].Continuation
				if c != nil && c.QuestionID == question.ID && c.DecisionBy == operations.DecisionUser && c.Status == operations.ContinuationWaiting {
					c.DecisionBy, c.Status = operations.DecisionModel, operations.ContinuationPending
					c.Version++
				}
			}
		}
		refreshStatus(op)
		appendEvent(op, operations.QuestionExpiredEvent, question.TaskID, question.ID, "Operator question expired without an answer")
		changed = true
	}
	return changed
}

func pendingQuestionDeadline(op operations.Operation) bool {
	for _, question := range op.Questions {
		if question.Pending() && !question.ExpiresAt.IsZero() {
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
		now := time.Now().UTC()
		due := false
		for _, question := range s.operation.Questions {
			if question.Pending() && !question.ExpiresAt.IsZero() && !now.Before(question.ExpiresAt) {
				due = true
				break
			}
		}
		if due {
			// A failed write leaves the previous snapshot intact for the next sweep.
			_ = r.update(ctx, s, func(next *operations.Operation) error {
				expireQuestions(next, now)
				return nil
			})
		}
	}
}
