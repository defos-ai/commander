package operations

import "time"

type EventType string

const (
	OperationStarted          EventType = "operation.started"
	PreflightUpdated          EventType = "operation.preflight_updated"
	TaskDispatched            EventType = "task.dispatched"
	ContinuationReady         EventType = "continuation.ready"
	ContinuationResolvedEvent EventType = "continuation.resolved"
	TaskAssigned              EventType = "task.assigned"
	TaskFinished              EventType = "task.finished"
	TaskInterruptedEvent      EventType = "task.interrupted"
	OperationRecovered        EventType = "operation.recovered"
	QuestionAsked             EventType = "question.asked"
	QuestionAnsweredEvent     EventType = "question.answered"
	QuestionExpiredEvent      EventType = "question.expired"
	OperationCompleted        EventType = "operation.completed"
	OperationFailed           EventType = "operation.failed"
	OperationCancelled        EventType = "operation.cancelled"
)

// Event is a cursor-addressable update. TaskID and QuestionID identify the
// corresponding record in Operation; large action results remain artifact refs.
type Event struct {
	Sequence    uint64    `json:"sequence"`
	OperationID string    `json:"operation_id"`
	Type        EventType `json:"type"`
	Status      Status    `json:"status"`
	TaskID      string    `json:"task_id,omitempty"`
	QuestionID  string    `json:"question_id,omitempty"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// AppendEvent records an event at the next operation-local sequence.
func (operation *Operation) AppendEvent(kind EventType, taskID, questionID, message string) {
	operation.Events = append(operation.Events, Event{Sequence: uint64(len(operation.Events) + 1),
		OperationID: operation.ID, Type: kind, Status: operation.Status, TaskID: taskID,
		QuestionID: questionID, Message: message, CreatedAt: time.Now().UTC()})
}
