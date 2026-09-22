// Package agents defines reported agent snapshots and their observed state.
package agents

import (
	"encoding/json"

	actionmodel "commander/framework/domain/actions"
)

type AgentStatus string

// AgentType identifies an extensible role, independently of an instance ID.
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

// AgentSnapshot is the public state reported by an agent. The controller adds
// liveness observations when storing it as an Agent.
type AgentSnapshot struct {
	ID                 string               `json:"id"`
	Type               AgentType            `json:"type"`
	Status             AgentStatus          `json:"status"`
	Server             ServerInfo           `json:"server"`
	Responsibilities   []string             `json:"responsibilities"`
	Actions            []actionmodel.Action `json:"actions"`
	GrantedPermissions []string             `json:"granted_permissions"`
	DiscoveryScope     *DiscoveryScope      `json:"discovery_scope"`
	Config             json.RawMessage      `json:"config"`
}
