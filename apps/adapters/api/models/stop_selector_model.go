package models

import (
	"encoding/json"
	"errors"
)

type StopAgentsRequest struct {
	Selector StopSelectorRequest `json:"selector"`
}

type StopSelectorRequest struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Version  string `json:"version,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

func (selector *StopSelectorRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || len(fields) == 0 {
		return errors.New("selector requires at least one exact filter")
	}
	var decoded StopSelectorRequest
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
			return errors.New("unknown stop selector filter")
		}
	}
	*selector = decoded
	return nil
}

type StopAgentsResponse struct {
	Items  []AgentResponse          `json:"items"`
	Errors []AgentStopErrorResponse `json:"errors,omitempty"`
}

type AgentStopErrorResponse struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}
