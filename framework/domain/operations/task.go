package operations

import (
	"encoding/json"
	"time"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

type TaskStatus string

const (
	TaskQueued      TaskStatus = "queued"
	TaskRunning     TaskStatus = "running"
	TaskSucceeded   TaskStatus = "succeeded"
	TaskFailed      TaskStatus = "failed"
	TaskCancelled   TaskStatus = "cancelled"
	TaskInterrupted TaskStatus = "interrupted"
)

// Task is one externally selected action assignment. Result is retained even if
// the operation is cancelled while its already-dispatched work finishes.
type Task struct {
	SessionIntent *sessions.Intent          `json:"session_intent,omitempty"`
	ID            string                    `json:"id"`
	OperationID   string                    `json:"operation_id"`
	ParentTaskID  string                    `json:"parent_task_id,omitempty"`
	AgentID       string                    `json:"agent_id"`
	ActionName    string                    `json:"action_name"`
	CommandID     string                    `json:"command_id,omitempty"`
	DeadlineAt    *time.Time                `json:"deadline_at,omitempty"`
	Input         json.RawMessage           `json:"input,omitempty"`
	Status        TaskStatus                `json:"status"`
	Result        *actionmodel.ActionOutput `json:"result,omitempty"`
	Continuation  *Continuation             `json:"continuation,omitempty"`
	Error         string                    `json:"error,omitempty"`
	StartedAt     time.Time                 `json:"started_at"`
	FinishedAt    *time.Time                `json:"finished_at,omitempty"`
}

func NewTask(operationID, parentID string, action actionmodel.NextAction) Task {
	taskID := id.TaskID()
	intent := action.SessionIntent.Clone()
	if intent != nil && intent.Mode == sessions.Create && intent.ID == "" {
		intent.ID = taskID
	}
	return Task{SessionIntent: intent, ID: taskID, CommandID: taskID, OperationID: operationID, ParentTaskID: parentID,
		ActionName: action.ActionName, Input: append([]byte(nil), action.Input...), Status: TaskQueued}
}

func (t Task) Clone() Task {
	t.SessionIntent = t.SessionIntent.Clone()
	t.Input = append(json.RawMessage(nil), t.Input...)
	if t.FinishedAt != nil {
		value := *t.FinishedAt
		t.FinishedAt = &value
	}
	if t.DeadlineAt != nil {
		value := *t.DeadlineAt
		t.DeadlineAt = &value
	}
	if t.Continuation != nil {
		continuation := t.Continuation.Clone()
		t.Continuation = &continuation
	}
	if t.Result != nil {
		result := t.Result.Clone()
		t.Result = &result
	}
	return t
}

func (t Task) NeedsRecovery() bool {
	return t.Status == TaskRunning || t.Status == TaskInterrupted
}
