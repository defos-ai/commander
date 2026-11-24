package operations

import (
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/domain/agents"
)

type TaskSelector struct {
	Hostname string `json:"hostname"`
}

func (selector TaskSelector) Validate() error {
	if selector.Hostname == "" {
		return errors.New("task selector requires hostname")
	}
	return (agents.ServerInfo{Hostname: selector.Hostname}).Validate()
}

func (selector TaskSelector) Matches(agent agents.Agent) bool {
	return selector.Hostname == agent.Server.Hostname
}

func (selector *TaskSelector) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return errors.New("task selector must be an object")
	}
	var next TaskSelector
	for name, raw := range fields {
		if name != "hostname" {
			return errors.New("unknown task selector field")
		}
		var value *string
		if err := json.Unmarshal(raw, &value); err != nil || value == nil || *value == "" {
			return errors.New("task selector hostname must be a nonempty string")
		}
		next.Hostname = *value
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*selector = next
	return nil
}
