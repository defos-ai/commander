package operations

import (
	"slices"

	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/missions"
)

func automaticRule(mission missions.Mission, task Task) (missions.Automation, bool) {
	rule, exists := mission.Automations[task.ActionName]
	if !exists || rule.Decide == missions.Model || rule.Validate() != nil || task.Status != TaskSucceeded || task.Continuation == nil {
		return missions.Automation{}, false
	}
	actions := task.Continuation.ProposedActions
	// A rule permits proposed work; it does not mean an empty result finishes
	// the branch. Leave that decision to the model so the operation can continue.
	if len(actions) == 0 {
		return missions.Automation{}, false
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
