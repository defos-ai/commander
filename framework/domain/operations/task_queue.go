package operations

import (
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/id"
)

// QueueTask returns false for an existing assignment, preserving any progress
// made since it was first queued. Automatic children are committed by their continuation.
func (o *Operation) QueueTask(task Task) (bool, error) {
	if !id.Valid(task.ID) || !id.Valid(task.OperationID) || task.OperationID != o.ID || task.CommandID != task.ID {
		return false, errors.New("task assignment requires matching operation and command identities")
	}
	if task.Status != TaskQueued || task.AgentID != "" || task.ParentTaskID != "" || !task.StartedAt.IsZero() || task.DeadlineAt != nil || task.FinishedAt != nil || task.Result != nil || task.Continuation != nil || task.Error != "" {
		return false, errors.New("direct task assignment requires a queued task without execution or continuation state")
	}
	if err := (actions.NextAction{ActionName: task.ActionName, Input: task.Input}).Validate(); err != nil {
		return false, err
	}
	if i := taskIndex(o, task.ID); i >= 0 {
		if sameAssignment(o.Tasks[i], task) {
			return false, nil
		}
		return false, ErrOperationConflict
	}
	if o.Status.Terminal() {
		return false, ErrFinished
	}
	for _, question := range o.Questions {
		if question.TaskID == "" && question.Pending() {
			return false, ErrQuestionPending
		}
	}
	if len(o.Tasks) >= 10000 {
		return false, errors.New("operation task limit exceeded")
	}

	task = task.Clone()
	o.Tasks = append(o.Tasks, task)
	o.RefreshStatus()
	o.AppendEvent(TaskAssigned, task.ID, "", "Task queued")
	o.UpdatedAt = time.Now().UTC()
	return true, nil
}

func sameAssignment(saved, incoming Task) bool {
	if !sameJSON(saved.SessionIntent, incoming.SessionIntent) {
		return false
	}
	if saved.OperationID != incoming.OperationID || saved.CommandID != incoming.CommandID || saved.ParentTaskID != incoming.ParentTaskID || saved.ActionName != incoming.ActionName {
		return false
	}
	return len(saved.Input) == 0 && len(incoming.Input) == 0 || sameJSON(saved.Input, incoming.Input)
}
