package fixtures

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	missionsmcp "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/framework/agent/packages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	operationfakes "github.com/defos-ai/commander/framework/tests/fakes/operation"
	operationfixtures "github.com/defos-ai/commander/framework/tests/fixtures/operation"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
	fssessions "github.com/defos-ai/commander/providers/fs/sessions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func HostMission() missions.Mission {
	return missions.Mission{
		Name: "review", Objectives: []string{"Review supplied evidence"},
		RequiredActions: []string{"inspect"},
		InputSchema:     json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		Policies: &policies.MissionPolicies{Continuation: &policies.ContinuationPolicy{ModelReview: []string{"inspect"}}},
	}
}

func Host(t *testing.T) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	database, storage := filepath.Join(root, "database"), filepath.Join(root, "storage")
	source := packages.NewClient(nil)
	t.Cleanup(source.CloseIdleConnections)
	config := missionsmcp.Config{
		SourceClient:      source,
		SessionStore:      hostStore(t, database, fssessions.NewSessionFsStore),
		AgentStore:        hostStore(t, database, fsagents.NewAgentFsStore),
		MissionStore:      hostStore(t, database, fsmissions.NewMissionFsStore),
		OperationStore:    hostStore(t, database, fsoperations.NewOperationFsStore),
		ExecutionStore:    hostStore(t, database, fsexecutions.NewExecutionFsStore),
		ReportStore:       hostStore(t, storage, fsreports.NewReportFsStore),
		ArtifactStore:     hostStore(t, database, fsartifacts.NewArtifactFsStore),
		ArtifactFileStore: hostStore(t, storage, fsartifacts.NewArtifactFileFsStore),
		ActionStore:       hostStore(t, database, fsactions.NewActionFsStore),
	}
	if err := config.MissionStore.Create(ctx, HostMission()); err != nil {
		t.Fatal(err)
	}
	dependencies := operationfixtures.Config()
	dependencies.MissionStore, dependencies.OperationStore, dependencies.ReportStore = config.MissionStore, config.OperationStore, config.ReportStore
	config.Operations = operationfixtures.NewWorker(t, dependencies, operationfakes.Executor{
		Run: func(_ context.Context, _ string, call actions.ActionCall) (actions.ActionOutput, error) {
			return operationfixtures.Output(call.ActionName), nil
		},
	})
	server, err := missionsmcp.NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "commander-local-tests", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return &Client{t: t, Context: ctx, Session: session}
}

func hostStore[T any](t *testing.T, root string, open func(string) (T, error)) T {
	t.Helper()
	value, err := open(root)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
