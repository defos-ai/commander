package models

import (
	"encoding/json"
	"time"
)

type AgentResponse struct {
	Sessions         []RuntimeSessionResponse     `json:"sessions,omitempty"`
	ID               string                       `json:"id"`
	Type             string                       `json:"type"`
	Version          string                       `json:"version,omitempty"`
	Status           string                       `json:"status"`
	StopCommandID    string                       `json:"stop_command_id,omitempty"`
	StopRequestedAt  time.Time                    `json:"stop_requested_at,omitzero"`
	Work             *AgentWorkResponse           `json:"work,omitempty"`
	Server           AgentServerResponse          `json:"server"`
	Responsibilities []string                     `json:"responsibilities"`
	Dependencies     []AgentDependencyResponse    `json:"dependencies,omitempty"`
	Actions          []ActionResponse             `json:"actions"`
	DiscoveryScope   *AgentDiscoveryScopeResponse `json:"discovery_scope"`
	Config           json.RawMessage              `json:"config"`
	ObservedAt       time.Time                    `json:"observed_at,omitzero"`
	LastSeen         time.Time                    `json:"last_seen"`
	LastHeartbeat    time.Time                    `json:"last_heartbeat"`
	LeaseExpiresAt   time.Time                    `json:"lease_expires_at,omitzero"`
	Stale            bool                         `json:"stale"`
}

type AgentDependencyResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
}

type AgentWorkResponse struct {
	OperationID         string                      `json:"operation_id,omitempty"`
	TaskID              string                      `json:"task_id,omitempty"`
	ActionName          string                      `json:"action_name"`
	CommandID           string                      `json:"command_id,omitempty"`
	MissingRequirements []ActionRequirementResponse `json:"missing_requirements,omitempty"`
}

type AgentServerResponse struct {
	IPAddress string `json:"ip_address"`
	Hostname  string `json:"hostname"`
}

type AgentDiscoveryScopeResponse struct {
	Paths    []string `json:"paths"`
	Services []string `json:"services"`
}
