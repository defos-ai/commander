package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	fakes "github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	adapterconsumer "github.com/defos-ai/commander/framework/controller/consumers/adapter"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/id"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type LateArtifact struct {
	Context    context.Context
	Session    *sdkmcp.ClientSession
	AgentID    string
	Executions executions.ExecutionStore
	Work       *fakes.LateArtifactAction
}

func NewLateArtifact(t *testing.T) *LateArtifact {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	fixture := &LateArtifact{Context: ctx, Work: fakes.NewLateArtifactAction(LateArtifactContents)}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	cleanupError := func(err error) {
		t.Helper()
		if err != nil {
			t.Errorf("late artifact fixture shutdown: %v", err)
		}
	}
	root := t.TempDir()
	database, storage := filepath.Join(root, "database"), filepath.Join(root, "storage")
	config := adapter.Config{}
	var err error
	config.AgentStore, err = fsagents.NewAgentFsStore(database)
	check(err)
	config.ActionStore, err = fsactions.NewActionFsStore(database)
	check(err)
	config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(database)
	check(err)
	config.ArtifactFileStore, err = fsartifacts.NewArtifactFileFsStore(storage)
	check(err)
	config.OperationStore, err = fsoperations.NewOperationFsStore(database)
	check(err)
	config.MissionStore, err = fsmissions.NewMissionFsStore(database)
	check(err)
	config.ReportStore, err = fsreports.NewReportFsStore(storage)
	check(err)
	executions, err := fsexecutions.NewExecutionFsStore(database)
	check(err)
	fixture.Executions = executions
	check(config.MissionStore.Upsert(ctx, missions.Mission{Name: "late-artifact", Objectives: []string{"Retain late execution evidence"},
		RequiredActions: []string{"late-artifact"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}))
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	check(err)
	t.Cleanup(func() { cleanupError(bus.Close()) })
	coordinator, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: config.AgentStore, ActionStore: config.ActionStore, ArtifactStore: config.ArtifactStore, ExecutionStore: executions,
	})
	check(err)
	t.Cleanup(func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		cleanupError(coordinator.Close(shutdown))
	})
	execution, err := runner.NewRunner(ctx, coordinator, runner.Config{AgentCatalog: coordinator,
		MissionStore: config.MissionStore, OperationStore: config.OperationStore, ReportStore: config.ReportStore, TaskTimeout: time.Second,
	})
	check(err)
	t.Cleanup(func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		cleanupError(execution.Close(shutdown))
	})
	consumer, err := adapterconsumer.NewConsumer(ctx, bus, messages.DefaultControllerType, coordinator.ID(), coordinator, execution, slog.New(slog.NewTextHandler(io.Discard, nil)))
	check(err)
	consumerCtx, cancelConsumer := context.WithCancel(ctx)
	consumerDone := make(chan error, 1)
	go func() { consumerDone <- consumer.Consume(consumerCtx) }()
	t.Cleanup(func() {
		cancelConsumer()
		select {
		case err := <-consumerDone:
			if !errors.Is(err, context.Canceled) {
				cleanupError(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("MCP fixture command consumer did not stop")
		}
		cleanupError(consumer.Close())
	})
	workerBus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	check(err)
	t.Cleanup(func() { cleanupError(workerBus.Close()) })
	heartbeat := 50 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{Type: "late-artifact-agent",
		Actions:           []action.Executor{fixture.Work},
		ArtifactFileStore: config.ArtifactFileStore, HeartbeatInterval: &heartbeat,
	})
	check(err)
	fixture.AgentID = worker.ID()
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Serve(workerCtx, workerBus, nil) }()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case err := <-workerDone:
			if !errors.Is(err, context.Canceled) {
				cleanupError(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("MCP fixture agent did not stop")
		}
	})
	t.Cleanup(fixture.Work.Unblock)
	adapterID, err := id.AdapterID("mcp")
	check(err)
	commands, err := controllercommands.NewClient(ctx, bus, adapterID, nil)
	check(err)
	t.Cleanup(func() { cleanupError(commands.Close()) })
	config.Operations = commands
	config.CatalogClient = packages.NewClient(nil)
	t.Cleanup(config.CatalogClient.CloseIdleConnections)
	server, err := adapter.NewServer(config)
	check(err)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	check(err)
	t.Cleanup(func() { cleanupError(serverSession.Close()) })
	fixture.Session, err = sdkmcp.NewClient(&sdkmcp.Implementation{Name: "late-artifact-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	check(err)
	t.Cleanup(func() { cleanupError(fixture.Session.Close()) })
	AwaitMCPLateArtifact(t, "ready agent", func() bool {
		record, err := config.AgentStore.Get(ctx, worker.ID())
		return err == nil && record.Status == "ready"
	})
	return fixture
}

func (f *LateArtifact) Call(t *testing.T, name string, input, output any) {
	t.Helper()
	MissionCall(t, f.Context, f.Session, name, input, output)
}

func AwaitMCPLateArtifact(t *testing.T, name string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const LateArtifactContents = "late source evidence\n"

func MissionCall(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, tool string, arguments, output any) string {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.StructuredContent != nil {
		t.Fatalf("%s must return one JSON text block without structured content: %+v", tool, result)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("%s did not return JSON text", tool)
	}
	if err := json.Unmarshal([]byte(content.Text), output); err != nil {
		t.Fatal(err)
	}
	return content.Text
}
