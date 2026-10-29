package models

import "time"

type LogResponse struct {
	ID          string    `json:"id"`
	AgentID     string    `json:"agent_id"`
	CommandID   string    `json:"command_id"`
	ActionName  string    `json:"action_name"`
	OperationID string    `json:"operation_id"`
	TaskID      string    `json:"task_id"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}
