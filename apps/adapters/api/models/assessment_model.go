package models

import (
	"encoding/json"
	"time"
)

type AssessmentSnapshot struct {
	TargetID   string          `json:"target_id"`
	TargetName string          `json:"target_name"`
	Revision   int             `json:"revision"`
	NetworkID  string          `json:"network_id,omitempty"`
	Scope      AssessmentScope `json:"scope"`
}
type AssessmentScope struct {
	Networks []AssessmentNetwork `json:"networks"`
	Domains  []AssessmentDomain  `json:"domains"`
}
type AssessmentNetwork struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	CIDRs []string `json:"cidrs"`
}
type AssessmentDomain struct {
	Hostname          string `json:"hostname"`
	IncludeSubdomains bool   `json:"include_subdomains"`
}

type ObservationBatchResponse struct {
	Version int                   `json:"version"`
	Records []ObservationResponse `json:"records"`
}
type ObservationIdentityResponse struct {
	Namespace string `json:"namespace"`
	Value     string `json:"value"`
	Evidence  string `json:"evidence"`
}
type ObservationEndpointResponse struct {
	NetworkID string `json:"network_id"`
	Address   string `json:"address,omitempty"`
}
type ObservationResponse struct {
	ID           string                       `json:"id"`
	Kind         string                       `json:"kind"`
	ObservedAt   *time.Time                   `json:"observed_at,omitempty"`
	NetworkID    string                       `json:"network_id,omitempty"`
	Address      string                       `json:"address,omitempty"`
	Hostname     string                       `json:"hostname,omitempty"`
	Identity     *ObservationIdentityResponse `json:"identity,omitempty"`
	Port         *int                         `json:"port,omitempty"`
	Transport    string                       `json:"transport,omitempty"`
	Protocol     string                       `json:"protocol,omitempty"`
	Product      string                       `json:"product,omitempty"`
	Version      string                       `json:"version,omitempty"`
	State        string                       `json:"state,omitempty"`
	IssueID      string                       `json:"issue_id,omitempty"`
	Title        string                       `json:"title,omitempty"`
	Severity     string                       `json:"severity,omitempty"`
	Description  string                       `json:"description,omitempty"`
	Evidence     string                       `json:"evidence,omitempty"`
	Source       *ObservationEndpointResponse `json:"source,omitempty"`
	Destination  *ObservationEndpointResponse `json:"destination,omitempty"`
	Relationship string                       `json:"relationship,omitempty"`
	Directed     *bool                        `json:"directed,omitempty"`
	Details      json.RawMessage              `json:"details,omitempty"`
}
