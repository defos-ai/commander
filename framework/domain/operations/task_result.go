package operations

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/id"
)

type TaskResult struct {
	TaskID     string
	AgentID    string
	Status     TaskStatus
	Result     *actions.ActionOutput
	Error      string
	FinishedAt time.Time
}

// FinishTask returns false for an already recorded outcome. Receipt timestamps
// do not identify results; redelivery must preserve the original completion.
func (o *Operation) FinishTask(result TaskResult) (bool, error) {
	if err := result.validate(); err != nil {
		return false, err
	}
	index := taskIndex(o, result.TaskID)
	if index < 0 {
		return false, ErrUnknownTask
	}
	task := &o.Tasks[index]
	if task.OperationID != o.ID || task.CommandID != task.ID || task.AgentID != result.AgentID {
		return false, ErrOperationConflict
	}
	if result.Result != nil && result.Result.ActionName != task.ActionName {
		return false, ErrOperationConflict
	}
	switch task.Status {
	case TaskSucceeded, TaskFailed:
		// A late observer timeout cannot replace a known execution outcome.
		if result.Status == TaskInterrupted || sameTaskResult(*task, result) {
			return false, nil
		}
		return false, ErrOperationConflict
	case TaskInterrupted:
		if sameTaskResult(*task, result) {
			return false, nil
		}
	case TaskRunning:
	default:
		return false, ErrOperationConflict
	}

	finished := result.FinishedAt.UTC()
	task.Status, task.Error, task.FinishedAt = result.Status, result.Error, &finished
	task.Result = nil
	if result.Result != nil {
		output := result.Result.Clone()
		task.Result = &output
	}
	continueTask(o, result.TaskID, result.Status == TaskSucceeded)
	refreshGroups(o)
	o.RefreshStatus()
	kind, message := TaskFinished, "Task finished; inspect its result or error"
	if result.Status == TaskInterrupted {
		kind, message = TaskInterruptedEvent, "Execution outcome is unknown; inspect before explicitly assigning replacement work"
	}
	o.AppendEvent(kind, result.TaskID, "", message)
	o.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (r TaskResult) validate() error {
	if !id.Valid(r.TaskID) || !id.ValidAgent(r.AgentID) || r.FinishedAt.IsZero() {
		return errors.New("task result requires valid task and agent IDs and a completion time")
	}
	switch r.Status {
	case TaskSucceeded:
		if r.Result == nil || r.Error != "" {
			return errors.New("successful task result requires output without an error")
		}
		return validateAgentResult(r.AgentID, *r.Result)
	case TaskFailed, TaskInterrupted:
		if r.Result != nil || strings.TrimSpace(r.Error) == "" {
			return errors.New("failed or interrupted task result requires an error without output")
		}
		return nil
	default:
		return errors.New("task result must be succeeded, failed, or interrupted")
	}
}

func validateAgentResult(agentID string, result actions.ActionOutput) error {
	if !id.ValidAgent(agentID) {
		return errors.New("invalid observation agent ID")
	}
	if err := result.Validate(); err != nil {
		return err
	}
	for _, ref := range result.Artifacts {
		if ref.AgentID != agentID || ref.ActionName != result.ActionName {
			return errors.New("observation artifact ownership does not match its agent and action")
		}
	}
	return nil
}

func sameTaskResult(task Task, result TaskResult) bool {
	return task.Status == result.Status && task.Error == result.Error && sameJSON(task.Result, result.Result)
}

func sameJSON(first, second any) bool {
	decode := func(output any) (any, error) {
		data, err := json.Marshal(output)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		err = decoder.Decode(&value)
		return value, err
	}
	previous, previousErr := decode(first)
	incoming, incomingErr := decode(second)
	return previousErr == nil && incomingErr == nil && reflect.DeepEqual(previous, incoming)
}
