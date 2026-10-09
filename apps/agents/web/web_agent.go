package web

import agents "github.com/defos-ai/commander/framework/agent"

const WebAgentType agents.AgentType = "web-agent"

type WebAgent struct {
	*agents.Agent
}

// NewWebAgent supplies a fresh HTTP catalog when config.Actions is
// nil. An explicitly empty catalog stays empty. It never grants permissions.
func NewWebAgent(config agents.AgentConfig) (*WebAgent, error) {
	config.Type = WebAgentType
	if config.Responsibilities == nil {
		config.Responsibilities = []string{"Discover website pages and inspect links, SEO metadata and technology indicators"}
	}
	if config.Actions == nil {
		config.Actions = NewActions()
	}
	agent, err := agents.NewAgent(config)
	if err != nil {
		return nil, err
	}
	return &WebAgent{Agent: agent}, nil
}
