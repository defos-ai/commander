package runtimes

import (
	"github.com/defos-ai/commander/framework/domain/actions"
)

// Result reports an observed registration announcement. It does not promise
// controller persistence, readiness of every action, or future availability.
type Result struct {
	AgentID     string           `json:"agent_id"`
	AgentType   string           `json:"agent_type"`
	Package     string           `json:"package"`
	Runtime     string           `json:"runtime"`
	PID         int              `json:"pid,omitempty"`
	ContainerID string           `json:"container_id,omitempty"`
	Actions     []actions.Action `json:"actions"`
}
