package models

import "time"

type ControllerResponse struct {
	ID             string                   `json:"id"`
	Type           string                   `json:"type"`
	Status         string                   `json:"status"`
	StatusReason   string                   `json:"status_reason,omitempty"`
	Server         ControllerServerResponse `json:"server"`
	Version        string                   `json:"version"`
	AgentSelector  string                   `json:"agent_selector"`
	StartedAt      time.Time                `json:"started_at"`
	LastHeartbeat  time.Time                `json:"last_heartbeat"`
	LeaseExpiresAt time.Time                `json:"lease_expires_at"`
	Stale          bool                     `json:"stale"`
}

type ControllerServerResponse struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}
