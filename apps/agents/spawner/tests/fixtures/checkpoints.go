package fixtures

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
)

func SpawnCheckpoints(t *testing.T) (context.Context, string, *fakes.Checkpoints) {
	t.Helper()
	command, err := messages.NewMessage(messages.RunAction, "spawner-agent-12345678",
		actions.ActionCall{ActionName: "spawn-agent", Input: json.RawMessage(`{"package":"github.com/defos-ai/fixture-agent@stable"}`)})
	if err != nil {
		t.Fatal(err)
	}
	journal := fakes.NewCheckpoints(command)
	ctx := action.WithExecution(t.Context(), action.Execution{CommandID: command.ID})
	return action.WithCheckpoints(ctx, journal), "command:" + command.ID, journal
}
