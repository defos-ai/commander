package spawner

import (
	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/framework/agent/action"
)

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
