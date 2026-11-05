package operations

import "time"

type EventType string

const (
	OperationStarted    EventType = "operation.started"
	PreflightUpdated    EventType = "operation.preflight_updated"
	OperationRecovered  EventType = "operation.recovered"
	OperationCompleted  EventType = "operation.completed"
	CompletionRequested EventType = "operation.completion_requested"
	OperationFailed     EventType = "operation.failed"
	OperationCancelled  EventType = "operation.cancelled"
)

const (
	TaskAssigned         EventType = "task.assigned"
	TaskDispatched       EventType = "task.dispatched"
	TaskFinished         EventType = "task.finished"
	TaskInterruptedEvent EventType = "task.interrupted"
	TaskBatchStarted     EventType = "task.batch.started"
	TaskBatchFinished    EventType = "task.batch.finished"
)

const (
	QuestionAsked         EventType = "question.asked"
	QuestionAnsweredEvent EventType = "question.answered"
)

const (
	ContinuationReady         EventType = "continuation.ready"
	ContinuationResolvedEvent EventType = "continuation.resolved"
)

type Event struct {
	Sequence    uint64    `json:"sequence"`
	OperationID string    `json:"operation_id"`
	Type        EventType `json:"type"`
	Status      Status    `json:"status"`
	TaskID      string    `json:"task_id,omitempty"`
	BatchID     string    `json:"batch_id,omitempty"`
	QuestionID  string    `json:"question_id,omitempty"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (operation *Operation) AppendEvent(kind EventType, taskID, questionID, message string) {
	operation.Events = append(operation.Events, Event{Sequence: uint64(len(operation.Events) + 1),
		OperationID: operation.ID, Type: kind, Status: operation.Status, TaskID: taskID,
		QuestionID: questionID, Message: message, CreatedAt: time.Now().UTC()})
}
