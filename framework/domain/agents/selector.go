package agents

import (
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/id"
)

type Selector struct {
	ID      string    `json:"id,omitempty"`
	Type    AgentType `json:"type,omitempty"`
	Version string    `json:"version,omitempty"`
}

func (selector Selector) Validate() error {
	if selector.ID == "" && selector.Type == "" && selector.Version == "" {
		return errors.New("agent selector requires id, type or version")
	}
	if selector.ID != "" && !id.ValidAgent(selector.ID) {
		return errors.New("invalid agent selector id")
	}
	if selector.Type != "" && !id.ValidType(string(selector.Type)) {
		return errors.New("invalid agent selector type")
	}
	return ValidateVersion(selector.Version)
}

func (selector Selector) Matches(agent Agent) bool {
	return (selector.ID == "" || selector.ID == agent.ID) &&
		(selector.Type == "" || selector.Type == agent.Type) &&
		(selector.Version == "" || selector.Version == agent.Version)
}

func (selector *Selector) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("agent selector must be an object")
	}
	var next Selector
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
