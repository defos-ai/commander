package operations

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

func (o Operation) ValidateCompletion(output json.RawMessage) error {
	if o.Status.Terminal() {
		return ErrFinished
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

func (o *Operation) Complete(reportID string, output json.RawMessage) error {
	if err := o.ValidateCompletion(output); err != nil {
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
	o.Output = append(json.RawMessage(nil), output...)
	o.ReportID, o.Status = reportID, Completed
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
