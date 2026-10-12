package operations

import (
	"slices"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
)

func automaticRule(mission missions.Mission, task Task) (missions.Automation, bool) {
	rule, exists := mission.Automations[task.ActionName]
	if !exists || rule.Decide == missions.Model || rule.Validate() != nil || task.Status != TaskSucceeded || task.Continuation == nil {
		return missions.Automation{}, false
	}
	actions := task.Continuation.ProposedActions
	// Closing an empty successful branch requires an explicit mission policy.
	if len(actions) == 0 {
		return rule, rule.OnEmpty == missions.ResolveEmpty
	}
	if err := actionmodel.ValidateNextActions(actions); err != nil {
		return missions.Automation{}, false
	}
	for _, action := range actions {
		if !slices.Contains(rule.Run, action.ActionName) {
			return missions.Automation{}, false
		}
	}
	return rule, true
}
