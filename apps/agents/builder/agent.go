package builder

import (
	"fmt"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
)

const AgentType agent.AgentType = "builder-agent"

// NewAgent supplies a fresh checkout catalog when config.Actions is nil.
// An explicitly empty catalog stays empty and does not open workspace storage.
func NewAgent(config agent.AgentConfig, workspaceDir string) (*agent.Agent, error) {
	config.Type = AgentType
	if config.Responsibilities == nil {
		config.Responsibilities = []string{"Prepare isolated source workspaces from the Commander example-agent template"}
	}
	if config.Actions == nil {
		var err error
		config.Actions, err = NewActions(workspaceDir)
		if err != nil {
			return nil, err
		}
	}
	return agent.NewAgent(config)
}

// NewActions creates fresh executors so agents never share discovery state.
func NewActions(workspaceDir string) ([]action.Executor, error) {
	checkout, err := builderactions.NewCheckoutTemplate(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("create checkout-template: %w", err)
	}
	return []action.Executor{checkout}, nil
}

// ActionNames lists the catalog without opening workspace storage.
func ActionNames() []string {
	return []string{builderactions.CheckoutTemplateName}
}
