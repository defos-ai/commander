package agents

import (
	"encoding/json"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

type AgentStatus string

type AgentType string

const GenericAgentType AgentType = "generic"

const (
	AgentIdle        AgentStatus = "idle"
	AgentDiscovering AgentStatus = "discovering"
	AgentReady       AgentStatus = "ready"
	AgentWorking     AgentStatus = "working"
	AgentUnavailable AgentStatus = "unavailable"
	AgentStopped     AgentStatus = "stopped"
)

// Work identifies the action being executed without exposing its inputs.
// Operation and task IDs are absent for work issued outside an operation.
type Work struct {
	OperationID string `json:"operation_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	StepID      string `json:"step_id,omitempty"`
	ActionName  string `json:"action_name"`
	CommandID   string `json:"command_id,omitempty"`
}

func (work *Work) Clone() *Work {
	if work == nil {
		return nil
	}
	copy := *work
	return &copy
}

// AgentSnapshot is the public state reported by an agent. The controller adds
// liveness observations when storing it as an Agent.
type AgentSnapshot struct {
	ID               string               `json:"id"`
	Type             AgentType            `json:"type"`
	Status           AgentStatus          `json:"status"`
	Work             *Work                `json:"work,omitempty"`
	Server           ServerInfo           `json:"server"`
	Responsibilities []string             `json:"responsibilities"`
	Actions          []actionmodel.Action `json:"actions"`
	DiscoveryScope   *DiscoveryScope      `json:"discovery_scope"`
	Config           json.RawMessage      `json:"config"`
}
