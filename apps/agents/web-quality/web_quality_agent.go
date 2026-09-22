// Package webquality provides website quality agents and their default action catalogs.
// Hosts configure permissions and runtime settings; controllers choose actions.
package webquality

import agents "commander/framework/agent"

const WebQualityAgentType agents.AgentType = "web-quality"

// WebQualityAgent collects website quality observations using controller-selected
// actions. It inherits discovery, execution, messaging, and artifacts from Agent.
type WebQualityAgent struct {
	*agents.Agent
}

// NewWebQualityAgent supplies a fresh website quality catalog when config.Actions is
// nil. An explicitly empty catalog stays empty. It never grants permissions.
func NewWebQualityAgent(config agents.AgentConfig) (*WebQualityAgent, error) {
	config.Type = WebQualityAgentType
	if config.Responsibilities == nil {
		config.Responsibilities = []string{"Review website links, basic accessibility, and desktop/mobile rendering"}
	}
	if config.Actions == nil {
		config.Actions = NewActions()
	}
	agent, err := agents.NewAgent(config)
	if err != nil {
		return nil, err
	}
	return &WebQualityAgent{Agent: agent}, nil
}
