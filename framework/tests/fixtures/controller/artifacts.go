package fixtures

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/id"
)

func ArtifactResult(t *testing.T) (messages.Message, messages.Message, actions.ActionOutput) {
	t.Helper()
	work := messages.WorkContext{OperationID: id.New(), TaskID: id.New()}
	call := actions.ActionCall{ActionName: "capture", Work: work, Input: json.RawMessage(`{"url":"https://example.test"}`)}
	command := messages.Message{ID: work.TaskID, Type: messages.RunAction, AgentID: AgentID, Work: work, Payload: JSON(t, call), CreatedAt: time.Now().UTC()}
	metadata := func(name, mediaType string) artifacts.Artifact {
		return artifacts.Artifact{ID: id.New(), Name: name, AgentID: AgentID, ActionName: call.ActionName, CommandID: command.ID, CreatedAt: time.Now().UTC(),
			Files: []artifacts.ArtifactFile{{ID: id.New(), Name: name, MediaType: mediaType, Size: 2, SHA256: strings.Repeat("a", 64)}}}
	}
	output := actions.ActionOutput{ActionName: call.ActionName, Output: json.RawMessage(`{}`), Artifacts: []artifacts.Artifact{
		metadata("desktop.png", "image/png"), metadata("mobile.png", "image/png"),
	}}
	result := messages.Message{ID: id.New(), Type: messages.ActionSucceeded, AgentID: AgentID, CorrelationID: command.ID, Work: work, Payload: JSON(t, output), CreatedAt: time.Now().UTC()}
	return command, result, output
}

func JSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
