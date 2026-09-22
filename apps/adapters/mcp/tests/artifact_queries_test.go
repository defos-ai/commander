package cli_test

import (
	"encoding/json"
	"testing"
	"time"

	"commander/framework/artifact"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	artifactprovider "commander/providers/artifact"
	databaseprovider "commander/providers/controller"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPRejectsArtifactsWithoutAcceptedExecutions(t *testing.T) {
	// Arrange: stored bytes and a saved task result are not sufficient evidence
	// of an accepted execution.
	d := newDeployment(t)
	database, err := databaseprovider.OpenDatabase(d.ctx, d.env["DATABASE_URL"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storage, err := artifactprovider.OpenStorage(d.ctx, d.env["STORAGE_URL"], artifactprovider.StorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	agentID, currentTaskID := "fixture-agent-1234abcd", id.New()
	put := func(commandID, content string) artifact.Ref {
		t.Helper()
		ref, err := storage.ArtifactStore.Put(d.ctx, artifact.Artifact{Name: "evidence.txt", MediaType: "text/plain", Data: []byte(content)}, artifact.Owner{
			AgentID: agentID, ActionName: "example", CommandID: commandID,
		})
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	unrelated := put(id.New(), "never reported")
	current := put(currentTaskID, "not accepted by an execution record")
	untrackedTaskID := id.New()
	untracked := put(untrackedTaskID, "saved without an execution identity")
	now := time.Now().UTC()
	operation := operations.Operation{
		ID: id.New(), Version: 1, Status: operations.Completed, CreatedAt: now, UpdatedAt: now,
		Mission: missions.Mission{Name: "artifact-acceptance", Objective: "saved observation", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
		Input:   json.RawMessage(`{}`), Output: json.RawMessage(`{}`),
	}
	operation.Tasks = []operations.Task{
		{ID: untrackedTaskID, OperationID: operation.ID, AgentID: agentID, ActionName: "example", Status: operations.TaskSucceeded,
			StartedAt: now, FinishedAt: &now, Result: &actionmodel.ActionOutput{ActionName: "example", Output: json.RawMessage(`{}`), Artifacts: []artifact.Ref{untracked}}},
		{ID: currentTaskID, CommandID: currentTaskID, OperationID: operation.ID, AgentID: agentID, ActionName: "example", Status: operations.TaskSucceeded,
			StartedAt: now, FinishedAt: &now, Result: &actionmodel.ActionOutput{ActionName: "example", Output: json.RawMessage(`{}`), Artifacts: []artifact.Ref{current}}},
	}
	if err := database.OperationStore.Create(d.ctx, operation); err != nil {
		t.Fatal(err)
	}
	endpoint, _ := d.mcpOnly(t, false)
	session := connect(t, d.ctx, endpoint)
	uri := func(ref artifact.Ref) string { return "missions://artifacts/" + ref.ID }

	// Act
	_, unrelatedErr := session.ReadResource(d.ctx, &mcp.ReadResourceParams{URI: uri(unrelated)})
	_, currentErr := session.ReadResource(d.ctx, &mcp.ReadResourceParams{URI: uri(current)})
	_, untrackedErr := session.ReadResource(d.ctx, &mcp.ReadResourceParams{URI: uri(untracked)})

	// Assert: operation records cannot bypass the execution acceptance check.
	if unrelatedErr == nil || currentErr == nil || untrackedErr == nil || d.controllerProcess != nil {
		t.Fatalf("untrusted artifacts became readable: unrelated=%v current=%v untracked=%v", unrelatedErr, currentErr, untrackedErr)
	}
}
