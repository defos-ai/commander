package fixtures

import (
	"testing"

	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/framework/agent/action"
)

func SpawnAction(t *testing.T, launcher spawneractions.Spawner) action.Executor {
	t.Helper()
	executor, err := spawneractions.NewSpawnAgent(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Discover(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	return executor
}
