package sessions

import (
	"errors"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

// Normalize resolves proposals before the operation transition creates children.
// An invalid automatic proposal remains visible and blocked; it cannot roll back
// its parent's accepted execution result. Explicit callers reject Error instead.
func Normalize(next []actions.NextAction, records []agents.Agent, definitions ...actions.Definition) ([]actions.NextAction, error) {
	result := actions.CloneNextActions(next)
	for i := range result {
		a := &result[i]
		a.SessionIntent = nil
		mode, err := sessionMode(a.ActionName, records, definitions)
		if err == nil {
			a.SessionIntent, err = model.ResolveIntent(mode, a.Input)
		}
		if err != nil {
			a.SessionIntent = &model.Intent{Error: err.Error()}
		}
	}
	return result, nil
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
