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
func Normalize(next []actions.NextAction, records []agents.Agent, definitions ...actions.Definition) []actions.NextAction {
	result := actions.CloneNextActions(next)
	for i := range result {
		a := &result[i]
		a.SessionIntent = nil
		mode := ""
		seen := false
		conflict := false
		for _, record := range records {
			for _, definition := range record.Actions {
				if definition.Name == a.ActionName {
					if seen && mode != definition.SessionMode {
						conflict = true
					}
					mode = definition.SessionMode
					seen = true
				}
			}
		}
		// A saved mission contract or learned catalog entry can resolve intent
		// before an executor returns. It supplies no readiness evidence.
		for _, definition := range definitions {
			if definition.Name != a.ActionName {
				continue
			}
			if seen && mode != definition.SessionMode {
				conflict = true
			}
			mode, seen = definition.SessionMode, true
		}
		var err error
		if !seen {
			err = errors.New("action_contract_unavailable: assign fresh work after the action is registered")
		} else if conflict {
			err = errors.New("conflicting session action contracts")
		} else {
			a.SessionIntent, err = model.ResolveIntent(mode, a.Input)
		}
		if err != nil {
			a.SessionIntent = &model.Intent{Error: err.Error()}
		}
	}
	return result
}
