package operations

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

const MaxFinalizationInputBytes = 8 << 20

// FinalizationInput gives an agent the accepted evidence for a settled graph.
// Domain interpretation belongs to that agent; raw task outputs are not copied.
type FinalizationInput struct {
	OperationID string             `json:"operation_id"`
	Input       json.RawMessage    `json:"input"`
	Tasks       []FinalizationTask `json:"tasks"`
}

type FinalizationTask struct {
	ID         string               `json:"id"`
	ActionName string               `json:"action_name"`
	Status     TaskStatus           `json:"status"`
	Error      string               `json:"error,omitempty"`
	Artifacts  []artifacts.Artifact `json:"artifacts,omitempty"`
}

func (o Operation) normalWorkSettled() bool {
	if o.Status.Terminal() {
		return false
	}
	for _, question := range o.Questions {
		if question.Pending() {
			return false
		}
	}
	count := 0
	for _, task := range o.Tasks {
		if task.ID == o.FinalizationTaskID {
			continue
		}
		count++
		if task.Status != TaskSucceeded || task.Result == nil || task.Continuation == nil || task.Continuation.Status != ContinuationResolved {
			return false
		}
	}
	return count > 0
}

func (o Operation) finalizationInput() FinalizationInput {
	input := FinalizationInput{OperationID: o.ID, Input: append(json.RawMessage(nil), o.Input...), Tasks: []FinalizationTask{}}
	for _, task := range o.Tasks {
		if task.ID == o.FinalizationTaskID {
			continue
		}
		record := FinalizationTask{ID: task.ID, ActionName: task.ActionName, Status: task.Status, Error: task.Error}
		if task.Result != nil {
			record.Artifacts = artifacts.CloneAll(task.Result.Artifacts)
		}
		input.Tasks = append(input.Tasks, record)
	}
	return input
}

// reconcileFinalization participates in the mutation that settles normal work.
// Recording the marker and assignment together prevents duplicate finalizers.
func (o *Operation) reconcileFinalization() bool {
	if o.Mission.Finalize == "" || o.FinalizationTaskID != "" || !o.normalWorkSettled() {
		return false
	}
	input, err := json.Marshal(o.finalizationInput())
	if err == nil && len(input) > MaxFinalizationInputBytes {
		err = fmt.Errorf("finalization input exceeds %d bytes", MaxFinalizationInputBytes)
	}
	if err == nil && len(o.Tasks) >= 10000 {
		err = ErrTaskLimitExceeded
	}
	if err != nil {
		// Preserve the accepted outcome while exposing the bounded-handoff failure.
		// A pending decision also prevents every recovery from retrying this error.
		task := &o.Tasks[len(o.Tasks)-1]
		task.Continuation.DecisionBy = DecisionModel
		task.Continuation.Status = ContinuationPending
		task.Continuation.Version++
		o.AppendEvent(ContinuationReady, task.ID, "", "Automatic finalization requires review: "+err.Error())
		return true
	}
	task := NewTask(o.ID, "", "", actions.NextAction{ActionName: o.Mission.Finalize, Input: input})
	o.FinalizationTaskID = task.ID
	o.Tasks = append(o.Tasks, task)
	o.AppendEvent(TaskAssigned, task.ID, "", "Settled work queued for automatic finalization")
	return true
}

// FinalizationOutput requires a successful finalizer and the unchanged settled
// graph. A task's transport success alone never proves the mission output valid.
func (o Operation) FinalizationOutput() (json.RawMessage, error) {
	if o.Mission.Finalize == "" || o.FinalizationTaskID == "" || !o.normalWorkSettled() {
		return nil, ErrUnresolvedWork
	}
	if err := o.validateFinalizationTask(); err != nil {
		return nil, err
	}
	i := taskIndex(&o, o.FinalizationTaskID)
	if i < 0 {
		return nil, ErrUnknownTask
	}
	task := o.Tasks[i]
	if task.ActionName != o.Mission.Finalize || task.Status != TaskSucceeded || task.Result == nil || task.FinishedAt == nil ||
		task.Continuation == nil || task.Continuation.Status != ContinuationResolved || len(task.Result.NextActions) != 0 {
		return nil, ErrUnresolvedWork
	}
	if err := o.Mission.ValidateOutput(o.Input, task.Result.Output); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), task.Result.Output...), nil
}

func (o Operation) validateFinalizationTask() error {
	if o.FinalizationTaskID == "" {
		return nil
	}
	i := taskIndex(&o, o.FinalizationTaskID)
	if i < 0 || o.Mission.Finalize == "" || o.Tasks[i].ActionName != o.Mission.Finalize || o.Tasks[i].ParentTaskID != "" {
		return errors.New("saved finalization marker does not identify its mission action")
	}
	if len(o.Tasks[i].Input) > MaxFinalizationInputBytes || !sameJSON(o.Tasks[i].Input, o.finalizationInput()) {
		return errors.New("saved finalization task does not match its accepted evidence snapshot")
	}
	return nil
}
