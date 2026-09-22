package providers_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentmodel "commander/framework/domain/agents"
	fsagents "commander/providers/fs/controller/agents"
)

func TestInventoryPersistsObservationsByAgentID(t *testing.T) {
	// Arrange
	root := t.TempDir()
	store, err := fsagents.NewAgentFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	// Agent types may contain the word controller.
	record := agentmodel.Agent{
		AgentSnapshot: agentmodel.AgentSnapshot{ID: "controller-agent-1234abcd", Type: "controller"},
		LastSeen:      time.Now().UTC(), LastHeartbeat: time.Now().UTC(),
	}
	if err := store.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}

	// Act
	reopened, err := fsagents.NewAgentFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := reopened.Get(t.Context(), record.ID)
	all, listErr := reopened.List(t.Context())
	data, fileErr := os.ReadFile(filepath.Join(root, "agents", record.ID+".json"))
	var snapshot map[string]json.RawMessage
	decodeErr := json.Unmarshal(data, &snapshot)

	// Assert
	if readErr != nil || got.ID != record.ID || got.Type != record.Type || !got.LastHeartbeat.Equal(record.LastHeartbeat) {
		t.Fatalf("agent did not survive reopen: %+v, %v", got, readErr)
	}
	if listErr != nil || len(all) != 1 || all[0].ID != record.ID {
		t.Fatalf("saved inventory = %+v, %v", all, listErr)
	}
	if fileErr != nil || decodeErr != nil || snapshot["controller_type"] != nil || snapshot["controller_id"] != nil || snapshot["name"] != nil {
		t.Fatalf("inventory has controller metadata or a separate name: %s, file=%v decode=%v", data, fileErr, decodeErr)
	}
}

func TestInventoryExpiryDoesNotRemoveConcurrentFreshHeartbeats(t *testing.T) {
	// Arrange
	root := t.TempDir()
	writer, err := fsagents.NewAgentFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	sweeper, err := fsagents.NewAgentFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC()
	record := agentmodel.Agent{
		AgentSnapshot: agentmodel.AgentSnapshot{ID: "web-quality-agent-1234abcd", Type: "web-quality"},
		LastSeen:      cutoff, LastHeartbeat: cutoff.Add(-time.Second),
	}
	if err := writer.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	record.LastSeen, record.LastHeartbeat = cutoff.Add(time.Second), cutoff.Add(time.Second)
	start := make(chan struct{})
	results := make(chan error, 2)

	// Act
	go func() { <-start; results <- writer.Upsert(t.Context(), record) }()
	go func() { <-start; _, err := sweeper.DeleteExpired(t.Context(), cutoff); results <- err }()
	close(start)
	writeOrSweepErr, sweepOrWriteErr := <-results, <-results
	got, readErr := writer.Get(t.Context(), record.ID)

	// Assert
	if writeOrSweepErr != nil || sweepOrWriteErr != nil || readErr != nil || !got.LastHeartbeat.Equal(record.LastHeartbeat) {
		t.Fatalf("fresh heartbeat was lost: %+v, write/sweep=%v/%v read=%v", got, writeOrSweepErr, sweepOrWriteErr, readErr)
	}
}
