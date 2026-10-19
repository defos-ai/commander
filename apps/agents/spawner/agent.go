package spawner

import (
	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	agents "github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
)

const AgentType agents.AgentType = "spawner-agent"

// NewAgent constructs a fresh spawn catalog without launching processes.
// An explicitly empty action catalog remains empty and does not require a launcher.
func NewAgent(config agents.AgentConfig, launcher spawneractions.Spawner) (*agents.Agent, error) {
	config.Type = AgentType
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
	return agents.NewAgent(config)
}

// NewActions creates executors with discovery state owned by this agent.
func NewActions(launcher spawneractions.Spawner) ([]action.Executor, error) {
	spawn, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		return nil, err
	}
	return []action.Executor{spawn}, nil
}

// ActionNames lists the catalog without creating providers or starting agents.
func ActionNames() []string {
	return []string{spawneractions.SpawnAgentName}
}
