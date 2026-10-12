package missions

import (
	"encoding/json"
	"fmt"

	"github.com/defos-ai/commander/framework/domain/actions"
)

// StartAction merges entry-action defaults with caller-supplied mission input.
// The operation retains the original mission input; these defaults configure
// the action and need not be part of the mission's public input schema.
func (m Mission) StartAction(input json.RawMessage) (actions.NextAction, error) {
	if m.Start == nil {
		return actions.NextAction{}, fmt.Errorf("mission has no automatic start")
	}
	defaults := map[string]json.RawMessage{}
	if len(m.Start.Input) != 0 {
		if err := json.Unmarshal(m.Start.Input, &defaults); err != nil || defaults == nil {
			return actions.NextAction{}, fmt.Errorf("start defaults must be a JSON object")
		}
	}
	var supplied map[string]json.RawMessage
	if err := json.Unmarshal(input, &supplied); err != nil || supplied == nil {
		return actions.NextAction{}, fmt.Errorf("automatic start requires a mission input object")
	}
	for name, value := range supplied {
		defaults[name] = value
	}
	merged, err := json.Marshal(defaults)
	if err != nil {
		return actions.NextAction{}, err
	}
	return actions.NextAction{ActionName: m.Start.ActionName, Input: merged, Reason: m.Start.Reason}, nil
}
