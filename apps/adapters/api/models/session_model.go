package models

import "time"

type RuntimeSessionResponse struct {
	Disposition   string     `json:"disposition,omitempty"`
	ReleaseReason string     `json:"release_reason,omitempty"`
	ID            string     `json:"id"`
	OwnerAgentID  string     `json:"owner_agent_id"`
	ScopeID       string     `json:"scope_id"`
	Kind          string     `json:"kind"`
	Label         string     `json:"label"`
	State         string     `json:"state"`
	Endpoint      string     `json:"endpoint,omitempty"`
	Peer          string     `json:"peer,omitempty"`
	Hostname      string     `json:"hostname,omitempty"`
	User          string     `json:"user,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	Busy          bool       `json:"busy"`
	Revision      uint64     `json:"revision"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ConnectedAt   *time.Time `json:"connected_at,omitempty"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
}

type ControlSessionRequest struct {
	ID           string `json:"id"`
	ScopeID      string `json:"scope_id"`
	OwnerAgentID string `json:"owner_agent_id"`
	Method       string `json:"method"`
	RequestID    string `json:"request_id,omitempty"`
}
