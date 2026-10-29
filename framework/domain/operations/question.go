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
)

type AnswerOrigin string

const (
	AnswerOperator AnswerOrigin = "operator"
	AnswerModel    AnswerOrigin = "model"
)

func (origin AnswerOrigin) Validate() error {
	if origin != AnswerOperator && origin != AnswerModel {
		return errors.New("answer origin must be operator or model")
	}
	return nil
}

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
	AnsweredBy AnswerOrigin   `json:"answered_by,omitempty"`
	AskedAt    time.Time      `json:"asked_at"`
	AnsweredAt *time.Time     `json:"answered_at,omitempty"`
	FallbackAt time.Time      `json:"fallback_at"`
}

// Pending questions block new work until an answer is saved.
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
	if q.Status != QuestionPending || q.Answer != "" || q.AnsweredAt != nil || q.AnsweredBy != "" {
		return errors.New("new question must be pending without an answer")
	}
	if q.AskedAt.IsZero() || !q.FallbackAt.After(q.AskedAt) {
		return errors.New("question requires an ask time and a later model fallback time")
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

func (o *Operation) AnswerQuestion(questionID, answer string, answeredBy AnswerOrigin) error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	if !id.Valid(questionID) || strings.TrimSpace(answer) == "" {
		return errors.New("answer requires a valid question ID and nonblank text")
	}
	if err := answeredBy.Validate(); err != nil {
		return err
	}
	for i := range o.Questions {
		question := &o.Questions[i]
		if question.ID != questionID {
			continue
		}
		switch question.Status {
		case QuestionAnswered:
			return ErrQuestionAnswered
		case QuestionPending:
		default:
			return errors.New("question is not pending")
		}
		now := time.Now().UTC()
		if question.FallbackAt.IsZero() {
			return errors.New("question has no model fallback time")
		}
		if answeredBy == AnswerModel && now.Before(question.FallbackAt) {
			return ErrQuestionOperatorWaiting
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
		question.Status, question.Answer, question.AnsweredAt, question.AnsweredBy = QuestionAnswered, answer, &now, answeredBy
		o.RefreshStatus()
		o.AppendEvent(QuestionAnsweredEvent, "", question.ID, "Answer received; planning can continue")
		o.UpdatedAt = time.Now().UTC()
		return nil
	}
	return ErrUnknownQuestion
}
