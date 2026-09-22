package providers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agents "commander/framework/agent"
	"commander/framework/artifact"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/domain/reports"
	artifactprovider "commander/providers/artifact"
	commsprovider "commander/providers/comms"
	controllerprovider "commander/providers/controller"
)

const (
	agentID     = "web-quality-agent-a5452d64"
	otherID     = "1d477a20-2af1-4c2b-b92a-1729a1c32f13"
	operationID = "87e11d18-d786-46a3-8d74-1a1bd5b9ad6f"
)

func fileURL(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func closeAfter(t *testing.T, closer io.Closer) func() {
	t.Helper()
	close := sync.OnceFunc(func() {
		if err := closer.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(close)
	return close
}

func TestBusURLConnectsIndependentClients(t *testing.T) {
	// Arrange
	root := filepath.Join(t.TempDir(), "shared bus")
	publisher, err := commsprovider.OpenBus(t.Context(), fileURL(root))
	if err != nil {
		t.Fatal(err)
	}
	closeAfter(t, publisher)
	receiver, err := commsprovider.OpenBus(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	closeAfter(t, receiver)
	subscription, err := receiver.Subscribe(t.Context(), "work")
	if err != nil {
		t.Fatal(err)
	}
	closeAfter(t, subscription)
	want, err := messages.NewMessage(messages.RunAction, agentID, map[string]string{"action": "capture"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	// Act
	if err := publisher.Publish(ctx, "work", want); err != nil {
		t.Fatal(err)
	}
	got, receiveErr := subscription.Next(ctx)
	ackErr := subscription.Ack(ctx, got.ID)

	// Assert
	if receiveErr != nil || ackErr != nil || got.ID != want.ID || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("shared bus delivery = %+v, receive=%v ack=%v; want %+v", got, receiveErr, ackErr, want)
	}
}

func TestStorageURLPersistsReportsAndScopesAgentArtifacts(t *testing.T) {
	// Arrange
	connection := fileURL(filepath.Join(t.TempDir(), "storage with spaces"))
	sender, err := artifactprovider.OpenStorage(t.Context(), connection, artifactprovider.StorageOptions{AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	closeSender := closeAfter(t, sender)
	reportWriter, err := controllerprovider.OpenReportStorage(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	closeReportWriter := closeAfter(t, reportWriter)
	input := artifact.Artifact{Name: "evidence.txt", Data: []byte("mission evidence")}
	owner := artifact.Owner{AgentID: agentID, ActionName: "capture"}
	wantReport := reports.Report{
		ID: otherID, OperationID: operationID, Mission: "capture-evidence",
		Output: json.RawMessage(`{"summary":"captured"}`), CreatedAt: time.Now().UTC(),
	}

	// Act
	ref, err := sender.ArtifactStore.Put(t.Context(), input, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := reportWriter.ReportStore.Put(t.Context(), wantReport); err != nil {
		t.Fatal(err)
	}
	closeSender()
	closeReportWriter()
	reader, err := artifactprovider.OpenStorage(t.Context(), connection, artifactprovider.StorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	closeAfter(t, reader)
	reportReader, err := controllerprovider.OpenReportStorage(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	closeAfter(t, reportReader)
	got, readErr := reader.ArtifactStore.Get(t.Context(), ref)
	report, reportErr := reportReader.ReportStore.Get(t.Context(), wantReport.ID)
	otherAgent, err := artifactprovider.OpenStorage(t.Context(), connection, artifactprovider.StorageOptions{AgentID: "other-agent-1d477a20"})
	if err != nil {
		t.Fatal(err)
	}
	closeAfter(t, otherAgent)
	foreignData, foreignReadErr := otherAgent.ArtifactStore.Get(t.Context(), ref)
	_, foreignWriteErr := otherAgent.ArtifactStore.Put(t.Context(), input, owner)

	// Assert
	if readErr != nil || !bytes.Equal(got, input.Data) {
		t.Fatalf("reopened artifact = %q, %v", got, readErr)
	}
	if reportErr != nil || report.OperationID != wantReport.OperationID || report.Mission != wantReport.Mission ||
		!bytes.Equal(report.Output, wantReport.Output) || !report.CreatedAt.Equal(wantReport.CreatedAt) {
		t.Fatalf("reopened report = %+v, %v; want %+v", report, reportErr, wantReport)
	}
	if foreignReadErr == nil || foreignData != nil || foreignWriteErr == nil {
		t.Fatalf("foreign artifact access: data=%q read=%v write=%v", foreignData, foreignReadErr, foreignWriteErr)
	}
}

func TestDatabaseURLPersistsControllerRecordsAcrossReopens(t *testing.T) {
	// Arrange
	connection := fileURL(filepath.Join(t.TempDir(), "database"))
	database, err := controllerprovider.OpenDatabase(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	closeDatabase := closeAfter(t, database)
	mission := missions.Mission{
		Name: "capture-evidence", Objective: "Capture evidence",
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	}
	operation := operations.Operation{
		ID: operationID, Mission: mission, Input: json.RawMessage(`{}`), Status: operations.Planning, Version: 1,
	}
	record := agentmodel.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: agentID, Type: agents.GenericAgentType},
		LastSeen:      time.Now().UTC(), LastHeartbeat: time.Now().UTC(),
	}
	description := actionmodel.Action{Name: "capture", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}

	// Act
	if err := database.MissionStore.Upsert(t.Context(), mission); err != nil {
		t.Fatal(err)
	}
	if err := database.OperationStore.Create(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	if err := database.AgentStore.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := database.ActionStore.Upsert(t.Context(), description); err != nil {
		t.Fatal(err)
	}
	closeDatabase()
	reopened, err := controllerprovider.OpenDatabase(t.Context(), connection)
	if err != nil {
		t.Fatal(err)
	}
	closeAfter(t, reopened)
	gotMission, missionErr := reopened.MissionStore.Get(t.Context(), mission.Name)
	gotOperation, operationErr := reopened.OperationStore.Get(t.Context(), operation.ID)
	gotAgent, agentErr := reopened.AgentStore.Get(t.Context(), record.ID)
	_, unknownAgentErr := reopened.AgentStore.Get(t.Context(), otherID)
	gotAction, actionErr := reopened.ActionStore.Get(t.Context(), description.Name)

	// Assert
	if missionErr != nil || gotMission.Name != mission.Name || gotMission.Objective != mission.Objective {
		t.Fatalf("reopened mission = %+v, %v", gotMission, missionErr)
	}
	if operationErr != nil || gotOperation.ID != operation.ID || gotOperation.Mission.Name != mission.Name ||
		gotOperation.Status != operation.Status || gotOperation.Version != operation.Version {
		t.Fatalf("reopened operation = %+v, %v", gotOperation, operationErr)
	}
	if agentErr != nil || gotAgent.ID != record.ID || gotAgent.Type != record.Type || !gotAgent.LastHeartbeat.Equal(record.LastHeartbeat) {
		t.Fatalf("reopened agent = %+v, %v", gotAgent, agentErr)
	}
	if !errors.Is(unknownAgentErr, agentmodel.ErrUnknownAgent) {
		t.Fatalf("found an unannounced agent: %v", unknownAgentErr)
	}
	if actionErr != nil || gotAction.Name != description.Name || !bytes.Equal(gotAction.InputSchema, description.InputSchema) {
		t.Fatalf("reopened action = %+v, %v", gotAction, actionErr)
	}
}

func TestFactoriesRejectInvalidURLsAndCancelledOpensWithoutCreatingFiles(t *testing.T) {
	factories := map[string]func(context.Context, string) (io.Closer, error){
		"bus": func(ctx context.Context, connection string) (io.Closer, error) {
			return commsprovider.OpenBus(ctx, connection)
		},
		"storage": func(ctx context.Context, connection string) (io.Closer, error) {
			return artifactprovider.OpenStorage(ctx, connection, artifactprovider.StorageOptions{})
		},
		"reports": func(ctx context.Context, connection string) (io.Closer, error) {
			return controllerprovider.OpenReportStorage(ctx, connection)
		},
		"database": func(ctx context.Context, connection string) (io.Closer, error) {
			return controllerprovider.OpenDatabase(ctx, connection)
		},
	}
	for name, open := range factories {
		t.Run(name, func(t *testing.T) {
			for _, scenario := range []string{"unsupported", "remote filesystem", "blank", "cancelled"} {
				t.Run(scenario, func(t *testing.T) {
					// Arrange
					root := filepath.Join(t.TempDir(), "must-not-exist")
					connection := fileURL(root)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					switch scenario {
					case "unsupported":
						connection = "mongodb://secret-user:secret-password@localhost/defos"
					case "remote filesystem":
						connection = (&url.URL{Scheme: "file", Host: "remote-host", Path: root}).String()
					case "blank":
						connection = "  "
					case "cancelled":
						cancel()
					}

					// Act
					resource, err := open(ctx, connection)
					_, statErr := os.Stat(root)

					// Assert
					if err == nil {
						_ = resource.Close()
						t.Fatal("invalid or cancelled connection was accepted")
					}
					if strings.Contains(err.Error(), "secret-") {
						t.Fatal("connection error exposed credentials")
					}
					if scenario == "cancelled" && !errors.Is(err, context.Canceled) {
						t.Fatalf("cancelled open = %v", err)
					}
					if !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("failed open created files: %v", statErr)
					}
				})
			}
		})
	}
}
