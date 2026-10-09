package browser

import (
	"sort"

	webactions "github.com/defos-ai/commander/apps/agents/browser/actions"
	"github.com/defos-ai/commander/framework/agent/action"
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
