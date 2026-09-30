package operations

import (
	"errors"
	"slices"
	"time"

	"commander/framework/id"
)

type TaskDispatch struct {
	TaskID     string
	AgentID    string
	StartedAt  time.Time
	DeadlineAt time.Time
}

func (o *Operation) DispatchTask(dispatch TaskDispatch) error {
	if !id.Valid(dispatch.TaskID) || !id.ValidAgent(dispatch.AgentID) {
		return errors.New("task dispatch requires valid task and agent IDs")
	}
	if dispatch.StartedAt.IsZero() || !dispatch.DeadlineAt.After(dispatch.StartedAt) {
		return errors.New("task dispatch requires a start time and a later deadline")
	}
	index := slices.IndexFunc(o.Tasks, func(task Task) bool { return task.ID == dispatch.TaskID })
	if index < 0 {
		return ErrUnknownTask
	}
	task := &o.Tasks[index]
	if o.Status.Terminal() || task.Status != TaskQueued || o.BlockedByQuestion(*task) || o.Preflight != nil && !o.Preflight.Ready {
		return ErrOperationConflict
	}
	if task.AgentID != "" {
		return ErrOperationConflict
	}
	if task.OperationID != o.ID || task.CommandID != task.ID {
		return errors.New("task dispatch requires matching operation and command identities")
	}

	deadline := dispatch.DeadlineAt.UTC()
	task.AgentID = dispatch.AgentID
	task.Status = TaskRunning
	task.StartedAt = dispatch.StartedAt.UTC()
	task.DeadlineAt = &deadline
	o.Status = Running
	o.UpdatedAt = time.Now().UTC()
	o.Events = append(o.Events, Event{
		Sequence: uint64(len(o.Events) + 1), OperationID: o.ID,
		Type: TaskDispatched, Status: o.Status, TaskID: task.ID,
		Message: "Task dispatched", CreatedAt: o.UpdatedAt,
	})
	return nil
}

func (o Operation) BlockedByQuestion(task Task) bool {
	for _, question := range o.Questions {
		if !question.Pending() {
			continue
		}
		if question.TaskID != "" {
			if task.ParentTaskID == question.TaskID {
				return true
			}
			continue
		}
		if question.StepID == "" || question.StepID == task.StepID {
			return true
		}
	}
	return false
}
