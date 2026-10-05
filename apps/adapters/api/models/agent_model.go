package models

import (
	"encoding/json"
	"time"
)

type AgentResponse struct {
	ID                 string                       `json:"id"`
	Type               string                       `json:"type"`
	Status             string                       `json:"status"`
	Work               *AgentWorkResponse           `json:"work,omitempty"`
	Server             AgentServerResponse          `json:"server"`
	Responsibilities   []string                     `json:"responsibilities"`
	Actions            []ActionResponse             `json:"actions"`
	GrantedPermissions []string                     `json:"granted_permissions"`
	DiscoveryScope     *AgentDiscoveryScopeResponse `json:"discovery_scope"`
	Config             json.RawMessage              `json:"config"`
	ObservedAt         time.Time                    `json:"observed_at,omitzero"`
	LastSeen           time.Time                    `json:"last_seen"`
	LastHeartbeat      time.Time                    `json:"last_heartbeat"`
	LeaseExpiresAt     time.Time                    `json:"lease_expires_at,omitzero"`
	Stale              bool                         `json:"stale"`
}

type AgentWorkResponse struct {
	OperationID string `json:"operation_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	StepID      string `json:"step_id,omitempty"`
	ActionName  string `json:"action_name"`
	CommandID   string `json:"command_id,omitempty"`
}

type AgentServerResponse struct {
	IPAddress string `json:"ip_address"`
	Hostname  string `json:"hostname"`
}

type AgentDiscoveryScopeResponse struct {
	Paths    []string `json:"paths"`
	Services []string `json:"services"`
}
