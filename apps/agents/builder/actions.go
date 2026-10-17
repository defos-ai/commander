package builder

import (
	agentactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent/action"
)

// NewActions creates fresh executors so agents never share discovery state.
func NewActions(workspaceDir string) ([]action.Executor, error) {
	return agentactions.NewActions(workspaceDir)
}

// ActionNames can be used by help/configuration without opening workspace storage.
func ActionNames() []string {
	return []string{agentactions.CheckoutTemplateName}
}
