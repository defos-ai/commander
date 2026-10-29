package fixtures

import (
	"encoding/json"
	"testing"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	executionfs "github.com/defos-ai/commander/providers/fs/executions"
)

func CheckpointStore(t *testing.T, root string) *executionfs.ExecutionFsStore {
	t.Helper()
	store, err := executionfs.NewExecutionFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func CheckpointMessages(t *testing.T) (messages.Message, messages.Message) {
	t.Helper()
	command, err := agentcommands.NewRunAction("test-agent-00000001", actions.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := messages.NewMessage(messages.ActionSucceeded, command.AgentID, actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	result.CorrelationID, result.Work = command.ID, command.Work
	return command, result
}
