package models

import "time"

type EventResponse struct {
	Sequence    uint64    `json:"sequence"`
	OperationID string    `json:"operation_id"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	TaskID      string    `json:"task_id,omitempty"`
	QuestionID  string    `json:"question_id,omitempty"`
	Message     string    `json:"message,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
