package operations

import "time"

// RefreshStatus derives the operation status without reopening a terminal operation.
func (operation *Operation) RefreshStatus() {
	if operation.Status.Terminal() {
		return
	}
	operation.Status = Planning
	for _, task := range operation.Tasks {
		if task.Status == TaskRunning {
			operation.Status = Running
			return
		}
	}
	if operation.Preflight != nil && !operation.Preflight.Ready {
		operation.Status = Waiting
		return
	}
	for _, question := range operation.Questions {
		if question.Pending() {
			operation.Status = Waiting
			return
		}
	}
	for _, task := range operation.Tasks {
		if task.Status == TaskQueued {
			operation.Status = Running
			return
		}
	}
}

// AppendEvent records an event at the next operation-local sequence.
func (operation *Operation) AppendEvent(kind EventType, taskID, questionID, message string) {
	operation.Events = append(operation.Events, Event{Sequence: uint64(len(operation.Events) + 1),
		OperationID: operation.ID, Type: kind, Status: operation.Status, TaskID: taskID,
		QuestionID: questionID, Message: message, CreatedAt: time.Now().UTC()})
}
