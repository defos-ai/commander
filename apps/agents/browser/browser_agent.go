package browser

import agents "github.com/defos-ai/commander/framework/agent"

const BrowserAgentType agents.AgentType = "browser-agent"

type BrowserAgent struct {
	*agents.Agent
}

// NewBrowserAgent supplies a fresh rendering catalog when config.Actions is
// nil. An explicitly empty catalog stays empty. It never grants permissions.
func NewBrowserAgent(config agents.AgentConfig) (*BrowserAgent, error) {
	config.Type = BrowserAgentType
	if config.Responsibilities == nil {
		config.Responsibilities = []string{"Check rendered accessibility and capture desktop/mobile screenshots"}
	}
	if config.Actions == nil {
		config.Actions = NewActions()
	}
	agent, err := agents.NewAgent(config)
	if err != nil {
		return nil, err
	}
	return &BrowserAgent{Agent: agent}, nil
}
