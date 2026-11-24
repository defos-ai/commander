package fixtures

import "github.com/defos-ai/commander/framework/domain/agents"

func ReadyAgentOnHost(id, hostname string) agents.Agent {
	agent := ReadyAgent(id)
	agent.Server.Hostname = hostname
	return agent
}
