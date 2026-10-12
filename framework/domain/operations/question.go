package operations

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

type QuestionStatus string

const (
	QuestionPending  QuestionStatus = "pending"
	QuestionAnswered QuestionStatus = "answered"
	QuestionExpired  QuestionStatus = "expired"
)

// A pending question blocks its StepID. An empty StepID blocks all new tasks.
// Work already assigned continues and its results remain available.
type Question struct {
	ID         string         `json:"id"`
	Status     QuestionStatus `json:"status"`
	StepID     string         `json:"step_id,omitempty"`
	TaskID     string         `json:"task_id,omitempty"`
	Text       string         `json:"text"`
	Options    []string       `json:"options,omitempty"`
	Answer     string         `json:"answer,omitempty"`
	AskedAt    time.Time      `json:"asked_at"`
	AnsweredAt *time.Time     `json:"answered_at,omitempty"`
	ExpiresAt  time.Time      `json:"expires_at"`
}

// Pending questions block new work until an answer or durable expiration.
func (q Question) Pending() bool { return q.Status == QuestionPending }

func (q Question) Clone() Question {
	q.Options = append([]string(nil), q.Options...)
	if q.AnsweredAt != nil {
		value := *q.AnsweredAt
		q.AnsweredAt = &value
	}
	return q
}

func (o *Operation) AskQuestion(question Question, version uint64) error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	if err := question.validateNew(); err != nil {
		return err
	}
	if err := o.ValidateStep(question.StepID); err != nil {
		return err
	}
	for _, existing := range o.Questions {
		if existing.ID == question.ID {
			return ErrOperationConflict
		}
	}
	if question.TaskID != "" {
		i := taskIndex(o, question.TaskID)
		if i < 0 {
			return ErrUnknownTask
		}
		c := o.Tasks[i].Continuation
		if version == 0 || c == nil || c.Version != version || c.Status != ContinuationPending || c.DecisionBy != DecisionModel {
			return fmt.Errorf("only a pending model decision can request operator intervention: %w", ErrContinuationConflict)
		}
		question.StepID = o.Tasks[i].StepID
		c.DecisionBy, c.Status, c.QuestionID = DecisionUser, ContinuationWaiting, question.ID
		c.Version++
	}
	question.Options = append([]string(nil), question.Options...)
	o.Questions = append(o.Questions, question)
	o.RefreshStatus()
	o.AppendEvent(QuestionAsked, question.TaskID, question.ID, "Operator intervention requested")
	o.UpdatedAt = time.Now().UTC()
	return nil
}

func (q Question) validateNew() error {
	if !id.Valid(q.ID) || q.TaskID != "" && !id.Valid(q.TaskID) {
		return errors.New("question and task IDs must be canonical UUIDs")
	}
	if q.Status != QuestionPending || q.Answer != "" || q.AnsweredAt != nil {
		return errors.New("new question must be pending without an answer")
	}
	if q.AskedAt.IsZero() || !q.ExpiresAt.After(q.AskedAt) {
		return errors.New("question requires an ask time and a later expiration")
	}
	if strings.TrimSpace(q.Text) == "" {
		return errors.New("question text must not be blank")
	}
	if len(q.Options) > 4 {
		return errors.New("questions support at most four options")
	}
	for _, option := range q.Options {
		if strings.TrimSpace(option) == "" {
			return errors.New("question options must not be blank")
		}
	}
	return nil
}

func (o *Operation) AnswerQuestion(questionID, answer string) error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	if !id.Valid(questionID) || strings.TrimSpace(answer) == "" {
		return errors.New("answer requires a valid question ID and nonblank text")
	}
	for i := range o.Questions {
		question := &o.Questions[i]
		if question.ID != questionID {
			continue
		}
		switch question.Status {
		case QuestionAnswered:
			return ErrQuestionAnswered
		case QuestionExpired:
			return ErrQuestionExpired
		case QuestionPending:
		default:
			return errors.New("question is not pending")
		}
		now := time.Now().UTC()
		if !question.ExpiresAt.IsZero() && !now.Before(question.ExpiresAt) {
			// Commit expiration without recording the late answer. The caller can
			// report rejection after the expired snapshot has been saved.
			o.ExpireQuestions(now)
			return nil
		}
		if question.TaskID != "" {
			i := taskIndex(o, question.TaskID)
			if i < 0 {
				return errors.New("question task is missing")
			}
			c := o.Tasks[i].Continuation
			if c == nil || c.QuestionID != question.ID || c.DecisionBy != DecisionUser || c.Status != ContinuationWaiting {
				return errors.New("question does not match the waiting continuation")
			}
			c.DecisionBy, c.Status = DecisionModel, ContinuationPending
			c.Version++
		}
		question.Status, question.Answer, question.AnsweredAt = QuestionAnswered, answer, &now
		o.reconcileFinalization()
		o.RefreshStatus()
		o.AppendEvent(QuestionAnsweredEvent, "", question.ID, "Answer received; planning can continue")
		o.UpdatedAt = time.Now().UTC()
		return nil
	}
	return ErrUnknownQuestion
}

// Expiration never invents an answer or resumes a finished operation.
func (o *Operation) ExpireQuestions(now time.Time) bool {
	changed := false
	for i := range o.Questions {
		question := &o.Questions[i]
		if !question.Pending() || question.ExpiresAt.IsZero() || now.Before(question.ExpiresAt) {
			continue
		}
		question.Status = QuestionExpired
		if !o.Status.Terminal() && question.TaskID != "" {
			if j := taskIndex(o, question.TaskID); j >= 0 {
				c := o.Tasks[j].Continuation
				if c != nil && c.QuestionID == question.ID && c.DecisionBy == DecisionUser && c.Status == ContinuationWaiting {
					c.DecisionBy, c.Status = DecisionModel, ContinuationPending
					c.Version++
				}
			}
		}
		o.RefreshStatus()
		o.AppendEvent(QuestionExpiredEvent, question.TaskID, question.ID, "Operator question expired without an answer")
		changed = true
	}
	if changed {
		o.reconcileFinalization()
		o.RefreshStatus()
		o.UpdatedAt = time.Now().UTC()
	}
	return changed
}
