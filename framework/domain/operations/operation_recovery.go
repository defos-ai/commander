package operations

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"commander/framework/id"
)

// Recover reconciles saved state without creating tasks or dispatching commands.
func (o *Operation) Recover(now time.Time) (bool, error) {
	if now.IsZero() {
		return false, errors.New("operation recovery requires a cutoff time")
	}
	if err := o.validateRecovery(); err != nil {
		return false, err
	}
	expired := o.ExpireQuestions(now)
	if o.Status.Terminal() && !slices.ContainsFunc(o.Tasks, Task.NeedsRecovery) {
		return expired, nil
	}
	o.RefreshStatus()
	o.AppendEvent(OperationRecovered, "", "", "Operation restored; existing commands are observed without being dispatched again")
	o.UpdatedAt = time.Now().UTC()
	return true, nil
}

func (o Operation) validateRecovery() error {
	switch o.Status {
	case Planning, Running, Waiting, Completed, Failed, Cancelled:
	default:
		return fmt.Errorf("invalid operation status %q", o.Status)
	}
	seen := make(map[string]bool)
	for _, task := range o.Tasks {
		undispatched := task.Status == TaskQueued || task.Status == TaskCancelled && task.DeadlineAt == nil
		if !id.Valid(task.ID) || (!undispatched || task.AgentID != "") && !id.ValidAgent(task.AgentID) || task.OperationID != o.ID || seen[task.ID] {
			return fmt.Errorf("invalid or duplicate task identity %q", task.ID)
		}
		seen[task.ID] = true
		if task.CommandID != task.ID {
			return fmt.Errorf("task %s has a mismatched command identity", task.ID)
		}
		if undispatched {
			continue
		}
		if task.DeadlineAt == nil || task.DeadlineAt.IsZero() {
			return fmt.Errorf("task %s has no execution deadline", task.ID)
		}
		switch task.Status {
		case TaskRunning, TaskSucceeded, TaskFailed, TaskCancelled, TaskInterrupted:
		default:
			return fmt.Errorf("task %s has an invalid status", task.ID)
		}
		if task.Result != nil {
			if task.Result.ActionName != task.ActionName {
				return fmt.Errorf("task %s result belongs to another action", task.ID)
			}
			if err := validateAgentResult(task.AgentID, *task.Result); err != nil {
				return fmt.Errorf("task %s result: %w", task.ID, err)
			}
		}
	}
	return nil
}
