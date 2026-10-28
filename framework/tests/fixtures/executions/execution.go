package fixtures

import (
	"encoding/json"
	"testing"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
)

func Execution(t *testing.T) executions.Execution {
	t.Helper()
	command, err := agentcommands.NewRunAction("example-agent-00000001", actions.ActionCall{
		ActionName: "inspect", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return executions.Execution{Command: command, StartedAt: command.CreatedAt}
}
