package agents

import (
	"encoding/json"
	"slices"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/sessions"
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
	OperationID         string                    `json:"operation_id,omitempty"`
	TaskID              string                    `json:"task_id,omitempty"`
	ActionName          string                    `json:"action_name"`
	CommandID           string                    `json:"command_id,omitempty"`
	MissingRequirements []actionmodel.Requirement `json:"missing_requirements,omitempty"`
}

func (work *Work) Clone() *Work {
	if work == nil {
		return nil
	}
	copy := *work
	copy.MissingRequirements = slices.Clone(work.MissingRequirements)
	return &copy
}

type AgentSnapshot struct {
	Sessions         []sessions.Session   `json:"sessions,omitempty"`
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
