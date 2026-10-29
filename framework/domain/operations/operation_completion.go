package operations

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/id"
)

type CompletionOrigin string

const (
	CompletionOperator CompletionOrigin = "operator"
	CompletionModel    CompletionOrigin = "model"
)

func (origin CompletionOrigin) Validate() error {
	if origin != CompletionOperator && origin != CompletionModel {
		return errors.New("completion origin must be operator or model")
	}
	return nil
}

func (o Operation) RequiresCompletionApproval() bool {
	return o.Mission.Completion != nil && o.Mission.Completion.DecisionBy == missions.Operator
}

func (o *Operation) SaveCompletionDraft(output json.RawMessage) error {
	if err := o.ValidateCompletionCandidate(output); err != nil {
		return err
	}
	if !o.RequiresCompletionApproval() {
		return errors.New("completion draft requires operator completion policy")
	}
	if !utf8.Valid(output) || !json.Valid(output) {
		return errors.New("completion draft must be valid UTF-8 JSON")
	}
	// Operation envelopes compact whitespace and escape HTML in RawMessage fields.
	previous, _ := json.Marshal(o.CompletionDraft)
	next, err := json.Marshal(output)
	if err != nil {
		return err
	}
	if len(o.CompletionDraft) != 0 && bytes.Equal(previous, next) {
		return nil
	}
	o.CompletionDraft = append(json.RawMessage(nil), output...)
	o.AppendEvent(CompletionRequested, "", "", "Awaiting operator approval")
	o.UpdatedAt = time.Now().UTC()
	return nil
}

func (o Operation) ValidateCompletion(output json.RawMessage, completedBy CompletionOrigin) error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	if err := completedBy.Validate(); err != nil {
		return err
	}
	if o.RequiresCompletionApproval() && completedBy != CompletionOperator {
		return ErrCompletionOperatorRequired
	}
	return o.ValidateCompletionCandidate(output)
}

func (o Operation) ValidateCompletionCandidate(output json.RawMessage) error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	if policy := o.Mission.Completion; policy != nil {
		if err := policy.Validate(); err != nil {
			return err
		}
	}
	for _, task := range o.Tasks {
		if task.Status == TaskQueued || task.Status == TaskRunning {
			return ErrActiveTasks
		}
	}
	for _, question := range o.Questions {
		if question.Pending() {
			return ErrQuestionPending
		}
	}
	if err := o.Mission.ValidateOutput(o.Input, output); err != nil {
		return err
	}
	for _, task := range o.Tasks {
		if c := task.Continuation; c != nil && c.Status != ContinuationResolved && c.DecisionBy != DecisionModel {
			return ErrUnresolvedWork
		}
	}
	return nil
}

func (o *Operation) Complete(reportID string, output json.RawMessage, completedBy CompletionOrigin) error {
	if err := o.ValidateCompletion(output, completedBy); err != nil {
		return err
	}
	if !id.Valid(reportID) {
		return errors.New("report ID must be a canonical UUID")
	}
	for i := range o.Tasks {
		if c := o.Tasks[i].Continuation; c != nil && c.Status != ContinuationResolved {
			c.Status = ContinuationResolved
			c.Version++
		}
	}
	o.CompletionDraft = nil
	o.Output = append(json.RawMessage(nil), output...)
	o.ReportID, o.Status, o.CompletedBy = reportID, Completed, completedBy
	o.AppendEvent(OperationCompleted, "", "", "Operation completed")
	o.UpdatedAt = time.Now().UTC()
	return nil
}

func (o *Operation) Fail(reason string) error {
	if o.Status.Terminal() {
		return ErrFinished
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("reason must not be blank")
	}
	o.Status, o.Error = Failed, reason
	o.cancelQueued()
	o.AppendEvent(OperationFailed, "", "", reason)
	o.UpdatedAt = time.Now().UTC()
	return nil
}
