package webquality

import (
	"sort"

	webactions "commander/apps/agents/web-quality/actions"
	"commander/framework/agent/action"
)

// NewActions creates the default catalog. Each call returns fresh instances so
// agents do not share mutable action discovery state.
func NewActions() []action.Executor {
	return webactions.NewActions()
}

func ActionNames() []string {
	actions := NewActions()
	names := make([]string, 0, len(actions))
	for _, instance := range actions {
		names = append(names, instance.Action().Name)
	}
	sort.Strings(names)
	return names
}
