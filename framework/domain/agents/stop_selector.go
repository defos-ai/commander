package agents

import (
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/id"
)

type StopSelector struct {
	ID       string    `json:"id,omitempty"`
	Type     AgentType `json:"type,omitempty"`
	Version  string    `json:"version,omitempty"`
	Hostname string    `json:"hostname,omitempty"`
}

func (selector StopSelector) Validate() error {
	if selector.ID == "" && selector.Type == "" && selector.Version == "" && selector.Hostname == "" {
		return errors.New("agent selector requires id, type, version or hostname")
	}
	if selector.ID != "" && !id.ValidAgent(selector.ID) {
		return errors.New("invalid agent selector id")
	}
	if selector.Type != "" && !id.ValidType(string(selector.Type)) {
		return errors.New("invalid agent selector type")
	}
	if err := (ServerInfo{Hostname: selector.Hostname}).Validate(); err != nil {
		return err
	}
	return ValidateVersion(selector.Version)
}

func (selector StopSelector) Matches(agent Agent) bool {
	return (selector.ID == "" || selector.ID == agent.ID) &&
		(selector.Type == "" || selector.Type == agent.Type) &&
		(selector.Version == "" || selector.Version == agent.Version) &&
		(selector.Hostname == "" || selector.Hostname == agent.Server.Hostname)
}

func (selector *StopSelector) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("agent selector must be an object")
	}
	var next StopSelector
	for name, raw := range fields {
		var value *string
		if err := json.Unmarshal(raw, &value); err != nil || value == nil || *value == "" {
			return errors.New("supplied agent selector fields must be nonempty strings")
		}
		switch name {
		case "id":
			next.ID = *value
		case "type":
			next.Type = AgentType(*value)
		case "version":
			next.Version = *value
		case "hostname":
			next.Hostname = *value
		default:
			return errors.New("unknown agent selector field")
		}
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*selector = next
	return nil
}
