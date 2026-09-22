package llm

import (
	"encoding/json"
	"errors"
	"strings"
)

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"parameters"`
}

type Call struct {
	ID, Name  string
	Arguments json.RawMessage
}

// Native preserves provider-specific tool signatures and reasoning fields when
// returning a complete assistant message to the same model on the next turn.
type Message struct {
	Role, Text   string
	Calls        []Call
	CallID, Name string
	Native       json.RawMessage
}

// Every protocol validates the complete set before returning executable calls.
// Keep the original arguments and Native payload intact for provider replay.
func validateCalls(calls []Call) error {
	seen := map[string]bool{}
	for _, call := range calls {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || seen[call.ID] {
			return errors.New("model returned an incomplete or duplicate tool call; no tool calls were executed")
		}
		seen[call.ID] = true
		var arguments map[string]json.RawMessage
		if err := json.Unmarshal(call.Arguments, &arguments); err != nil || arguments == nil {
			return errors.New("model returned invalid tool arguments: expected a JSON object; no tool calls were executed")
		}
	}
	return nil
}
