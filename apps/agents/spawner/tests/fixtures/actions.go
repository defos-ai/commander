package fixtures

import (
	"encoding/json"
	"testing"

	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/framework/agent/action"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
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

func UnavailableSpawnAction() actionmodel.Action {
	return actionmodel.Action{
		SessionMode: "open", Name: "fixture-action", Description: "Run a fixture action in its own session.",
		Status: actionmodel.Unavailable, StatusReason: "Install fixture-runtime or configure FIXTURE_RUNTIME_PATH",
		MissingRequirements: []actionmodel.Requirement{{Kind: actionmodel.ExecutableRequirement, Name: "fixture-runtime"}},
		RequiredSecrets:     []string{"FIXTURE_API_KEY"},
		InputSchema:         json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
		OutputSchema:        json.RawMessage(`{"type":"object","properties":{"result":{"type":"string"}}}`),
	}
}
