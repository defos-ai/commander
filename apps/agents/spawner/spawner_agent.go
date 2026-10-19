package spawner

import (
	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	agents "github.com/defos-ai/commander/framework/agent"
)

const SpawnerAgentType agents.AgentType = "spawner-agent"

type SpawnerAgent struct {
	*agents.Agent
}

// NewSpawnerAgent constructs a fresh spawn catalog without launching processes.
// An explicitly empty action catalog remains empty and does not require a launcher.
func NewSpawnerAgent(config agents.AgentConfig, launcher spawneractions.Spawner) (*SpawnerAgent, error) {
	config.Type = SpawnerAgentType
	if config.Responsibilities == nil {
		config.Responsibilities = []string{"Launch versioned agent packages and observe their registration"}
	}
	if config.Actions == nil {
		var err error
		config.Actions, err = NewActions(launcher)
		if err != nil {
			return nil, err
		}
	}
	instance, err := agents.NewAgent(config)
	if err != nil {
		return nil, err
	}
	return &SpawnerAgent{Agent: instance}, nil
}
