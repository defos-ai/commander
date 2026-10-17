package builder

import agents "github.com/defos-ai/commander/framework/agent"

const BuilderAgentType agents.AgentType = "builder-agent"

type BuilderAgent struct {
	*agents.Agent
}

// NewBuilderAgent supplies a fresh checkout catalog when config.Actions is nil.
// An explicitly empty catalog stays empty and does not open workspace storage.
func NewBuilderAgent(config agents.AgentConfig, workspaceDir string) (*BuilderAgent, error) {
	config.Type = BuilderAgentType
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
	agent, err := agents.NewAgent(config)
	if err != nil {
		return nil, err
	}
	return &BuilderAgent{Agent: agent}, nil
}
