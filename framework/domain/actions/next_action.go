package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// NextAction proposes follow-up work. It does not authorize or dispatch an action.
// The controller selects an available agent to execute it.
type NextAction struct {
	ActionName string          `json:"action_name"`
	Input      json.RawMessage `json:"input,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

func (a NextAction) Validate() error {
	if strings.TrimSpace(a.ActionName) == "" {
		return errors.New("next action action_name must not be blank")
	}
	if len(a.Input) != 0 {
		input := bytes.TrimSpace(a.Input)
		if len(input) == 0 || input[0] != '{' || !json.Valid(input) {
			return errors.New("next action input must be a JSON object")
		}
	}
	return nil
}

func ValidateNextActions(actions []NextAction) error {
	for i, action := range actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("next_actions[%d]: %w", i, err)
		}
	}
	return nil
}

func CloneNextActions(actions []NextAction) []NextAction {
	if actions == nil {
		return nil
	}
	cloned := append([]NextAction{}, actions...)
	for i := range cloned {
		cloned[i].Input = bytes.Clone(cloned[i].Input)
	}
	return cloned
}
