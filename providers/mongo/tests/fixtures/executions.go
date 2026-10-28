package fixtures

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
)

func ExecutionCommand(index int) messages.Message {
	work := messages.WorkContext{OperationID: "10000000-0000-4000-8000-000000000001", TaskID: "20000000-0000-4000-8000-000000000001", StepID: "review"}
	payload, _ := json.Marshal(actions.ActionCall{ActionName: "check", Work: work, Input: json.RawMessage(`{"serial":9007199254740993,"site":"example.test"}`)})
	return messages.Message{ID: fmt.Sprintf("00000000-0000-4000-8000-%012x", index), Type: messages.RunAction, AgentID: "browser-agent-00000001", Work: work, Payload: payload, CreatedAt: time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)}
}

func ExecutionResult(command messages.Message) messages.Message {
	return messages.Message{ID: "30000000" + command.ID[8:], Type: messages.ActionSucceeded, AgentID: command.AgentID, CorrelationID: command.ID, Work: command.Work, Payload: json.RawMessage(`{"action_name":"check","output":{"serial":9007199254740993}}`), CreatedAt: command.CreatedAt.Add(time.Nanosecond)}
}

func ExecutionArtifact(command messages.Message, index int) artifacts.Artifact {
	return artifacts.Artifact{
		ID: fmt.Sprintf("40000000-0000-4000-8000-%012x", index), Name: "result",
		AgentID: command.AgentID, ActionName: "check", CommandID: command.ID,
		CreatedAt: command.CreatedAt.Add(2 * time.Nanosecond),
		Files: []artifacts.ArtifactFile{{
			ID: fmt.Sprintf("50000000-0000-4000-8000-%012x", index), Name: "result.json",
			MediaType: "application/json", Size: 2, SHA256: strings.Repeat("a", 64),
		}},
	}
}
