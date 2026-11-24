package models

import (
	"encoding/json"
	"errors"
)

type StopAgentsRequest struct {
	Selector AgentSelectorRequest `json:"selector"`
}

type AgentSelectorRequest struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Version  string `json:"version,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

func (selector *AgentSelectorRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) == 0 {
		return errors.New("selector requires at least one exact filter")
	}
	var decoded AgentSelectorRequest
	for name, raw := range fields {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil || value == "" {
			return errors.New("selector filters must be nonempty strings")
		}
		switch name {
		case "id":
			decoded.ID = value
		case "type":
			decoded.Type = value
		case "version":
			decoded.Version = value
		case "hostname":
			decoded.Hostname = value
		default:
			return errors.New("unknown agent selector filter")
		}
	}
	*selector = decoded
	return nil
}

type AgentSelectorResponse struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Version  string `json:"version,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

type StopAgentsResponse struct {
	Items  []AgentResponse          `json:"items"`
	Errors []AgentStopErrorResponse `json:"errors,omitempty"`
}

type AgentStopErrorResponse struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}
