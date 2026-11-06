package sessions

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

// Normalize resolves proposals before the operation transition creates children.
// An invalid automatic proposal remains visible and blocked; it cannot roll back
// its parent's accepted execution result. Explicit callers reject Error instead.
func Normalize(parent *operations.Task, next []actions.NextAction, records []agents.Agent, definitions ...actions.Definition) ([]actions.NextAction, error) {
	result := actions.CloneNextActions(next)
	for i := range result {
		a := &result[i]
		a.SessionIntent = nil
		mode, err := sessionMode(a.ActionName, records, definitions)
		if err == nil {
			a.SessionIntent, err = resolveTaskIntent(parent, mode, a.Input)
		}
		if err != nil {
			a.SessionIntent = &model.Intent{Error: err.Error()}
		}
	}
	return result, nil
}

func resolveTaskIntent(parent *operations.Task, mode string, input json.RawMessage) (*model.Intent, error) {
	if mode == "" || parent == nil {
		return model.ResolveIntent(mode, input)
	}
	if mode != "open" && mode != "use" {
		return nil, errors.New("invalid session mode")
	}
	if parent.SessionIntent == nil {
		if mode == "use" {
			return nil, errors.New("session_binding_required: direct parent has no session to use")
		}
		intent, err := model.ResolveIntent(mode, input)
		if err == nil && intent.ID != "" {
			return nil, errors.New("session_binding_required: direct parent has no session to reuse")
		}
		return intent, err
	}
	if err := parent.SessionIntent.Validate(); err != nil {
		return nil, fmt.Errorf("invalid parent session: %w", err)
	}
	intent := parent.SessionIntent.Clone()
	intent.Mode = model.Mode(mode)
	if err := checkSessionReferences(input, intent); err != nil {
		return nil, err
	}
	return intent, nil
}

func checkSessionReferences(input json.RawMessage, intent *model.Intent) error {
	if len(input) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return err
	}
	for name, value := range fields {
		expected := ""
		switch {
		case strings.EqualFold(name, "session_scope"):
			expected = intent.ScopeID
		case strings.EqualFold(name, "session_id"):
			expected = intent.ID
		default:
			continue
		}
		var actual string
		if err := json.Unmarshal(value, &actual); err != nil || actual != expected {
			return fmt.Errorf("session_binding_conflict: %s must match the direct parent's session", name)
		}
	}
	return nil
}

func sessionMode(name string, records []agents.Agent, definitions []actions.Definition) (string, error) {
	mode, seen, conflict := "", false, false
	accept := func(candidate string) {
		conflict = conflict || seen && mode != candidate
		mode, seen = candidate, true
	}
	for _, record := range records {
		for _, definition := range record.Actions {
			if definition.Name == name {
				accept(definition.SessionMode)
			}
		}
	}
	// Mission and learned contracts resolve intent without granting readiness.
	for _, definition := range definitions {
		if definition.Name == name {
			accept(definition.SessionMode)
		}
	}
	if !seen {
		return "", errors.New("action_contract_unavailable: assign fresh work after the action is registered")
	}
	if conflict {
		return "", errors.New("conflicting session action contracts")
	}
	return mode, nil
}
