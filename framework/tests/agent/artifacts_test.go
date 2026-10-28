package agent_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
)

func TestAgentPublishesArtifactFiles(t *testing.T) {
	data := []byte(strings.Repeat("evidence ", 20_000))
	summary := json.RawMessage(`{"items":20000}`)
	execute := fakes.NewArtifactAction(action.Result{
		Output:    summary,
		Artifacts: []action.Artifact{{Name: "Evidence", Files: []artifacts.FileSource{{Name: "evidence.txt", MediaType: "text/plain", Data: data}}}},
	})
	files := &fakes.OutputFiles{}
	instance, err := agent.NewAgent(agent.AgentConfig{Type: "fixture", Actions: []action.Executor{execute}, ArtifactFileStore: files})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.DiscoverActions(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	result, err := instance.Work(t.Context(), agent.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{}`)})

	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Output, summary) || len(result.Artifacts) != 1 {
		t.Fatalf("action lost summary or artifact: %+v", result)
	}
	record := result.Artifacts[0]
	if record.AgentID != instance.ID() || record.ActionName != "inspect" || len(record.Files) != 1 || record.ID != files.ArtifactID() {
		t.Fatalf("artifact lost ownership or file identity: %+v", record)
	}
	content, err := files.Get(t.Context(), record.ID, record.Files[0])
	if err != nil || !bytes.Equal(content, data) {
		t.Fatalf("artifact file could not be read: %v", err)
	}
	message, err := json.Marshal(result)
	if err != nil || len(message) >= len(data) || bytes.Contains(message, data) {
		t.Fatalf("file content leaked into action result: size=%d, %v", len(message), err)
	}
	copy := result.Clone()
	copy.Artifacts[0].Files[0].Name = "changed"
	copy.Output[0] = '['
	if result.Artifacts[0].Files[0].Name != "evidence.txt" || !bytes.Equal(result.Output, summary) {
		t.Fatal("result data was shared across clones")
	}
	uploadFailure := errors.New("storage unavailable")
	files.Fail(uploadFailure)
	failed, err := instance.Work(t.Context(), agent.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{}`)})
	if !errors.Is(err, actions.ErrArtifactPersistence) || !errors.Is(err, uploadFailure) || len(failed.Artifacts) != 0 || len(failed.Output) != 0 {
		t.Fatalf("failed upload produced successful output: %+v, %v", failed, err)
	}
}
