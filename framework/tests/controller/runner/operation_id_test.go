package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	provider "commander/providers/controller"
)

func TestStartOperationWithCallerID(t *testing.T) {
	// Arrange: exercise the framework directly with real stores, without MCP validation.
	database, err := provider.OpenDatabase(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storage, err := provider.OpenReportStorage(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	mission := missions.Mission{Name: "review", Objective: "Review", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := database.MissionStore.Upsert(t.Context(), mission); err != nil {
		t.Fatal(err)
	}
	r, err := runner.NewRunner(t.Context(), &waitingExecutor{}, runner.Config{
		MissionStore: database.MissionStore, OperationStore: database.OperationStore,
		ReportStore: storage.ReportStore, ArtifactRestorer: artifactIndex{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	const suppliedID = "f9181c24-68aa-4aac-8c34-89f8d41a2379"
	request := commands.StartOperationCommand{OperationID: suppliedID, MissionName: mission.Name, Input: json.RawMessage(`{"target":"example.com"}`)}

	// Act: reject malformed IDs, accept a caller ID, and retry it with different input.
	for _, invalid := range []string{"not-a-uuid", "../operations", strings.ToUpper(suppliedID), "{" + suppliedID + "}", " " + suppliedID, strings.ReplaceAll(suppliedID, "-", "")} {
		bad := request
		bad.OperationID = invalid
		if _, err := r.Start(t.Context(), bad); err == nil || !strings.Contains(err.Error(), "operation_id") {
			t.Fatalf("invalid ID %q: %v", invalid, err)
		}
	}
	started, err := r.Start(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	before, err := database.OperationStore.Get(t.Context(), suppliedID)
	if err != nil {
		t.Fatal(err)
	}
	_, duplicateErr := r.Start(t.Context(), request)
	request.Input = json.RawMessage(`{"target":"other.example"}`)
	_, conflictErr := r.Start(t.Context(), request)
	request.OperationID = ""
	generated, generatedErr := r.Start(t.Context(), request)
	stored, readErr := database.OperationStore.Get(t.Context(), suppliedID)
	all, listErr := database.OperationStore.List(t.Context())

	// Assert: the caller owns the durable ID; duplicate starts never replace state.
	if started.ID != suppliedID || stored.ID != suppliedID || !reflect.DeepEqual(stored, before) || readErr != nil {
		t.Fatalf("caller ID or saved state changed: started=%+v stored=%+v err=%v", started, stored, readErr)
	}
	if !errors.Is(duplicateErr, operations.ErrOperationConflict) || !errors.Is(conflictErr, operations.ErrOperationConflict) {
		t.Fatalf("expected duplicate conflicts: %v, %v", duplicateErr, conflictErr)
	}
	if generatedErr != nil || !id.Valid(generated.ID) || generated.ID == suppliedID || generated.ID[14] != '4' {
		t.Fatalf("omitted ID did not generate a UUIDv4: %+v, %v", generated, generatedErr)
	}
	if listErr != nil || len(all) != 2 {
		t.Fatalf("invalid or duplicate requests created extra operations: %+v, %v", all, listErr)
	}
}
