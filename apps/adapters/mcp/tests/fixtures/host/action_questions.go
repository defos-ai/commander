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
	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
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

type ActionQuestion struct {
	Context    context.Context
	Session    *sdkmcp.ClientSession
	AgentID    string
	Executions executions.ExecutionStore
	Work       *fakes.QuestionAction
}

func NewActionQuestion(t *testing.T) *ActionQuestion {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	closeError := func(err error) {
		t.Helper()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("question fixture shutdown: %v", err)
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
	store, err := fsoperations.NewOperationFsStore(database)
	check(err)
	config.OperationStore = fakes.QuestionWindowStore{OperationStore: store, Window: 3 * time.Second}
	config.MissionStore, err = fsmissions.NewMissionFsStore(database)
	check(err)
	config.ReportStore, err = fsreports.NewReportFsStore(storage)
	check(err)
	config.ExecutionStore, err = fsexecutions.NewExecutionFsStore(database)
	check(err)
	check(config.MissionStore.Upsert(ctx, missions.Mission{Name: "action-question", Objectives: []string{"Continue one action after its saved answer"},
		RequiredActions: []string{"confirm-action"}, InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`)}))
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	check(err)
	t.Cleanup(func() { closeError(bus.Close()) })
	coordinator, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: config.AgentStore, ActionStore: config.ActionStore, ArtifactStore: config.ArtifactStore,
		ExecutionStore: config.ExecutionStore, OperationStore: config.OperationStore,
	})
	check(err)
	t.Cleanup(func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		closeError(coordinator.Close(shutdown))
	})
	execution, err := runner.NewRunner(ctx, coordinator, runner.Config{AgentCatalog: coordinator,
		MissionStore: config.MissionStore, OperationStore: config.OperationStore, ReportStore: config.ReportStore, TaskTimeout: 10 * time.Second,
	})
	check(err)
	t.Cleanup(func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		closeError(execution.Close(shutdown))
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
			closeError(err)
		case <-time.After(5 * time.Second):
			t.Error("question command consumer did not stop")
		}
		closeError(consumer.Close())
	})
	work := fakes.NewQuestionAction()
	heartbeat := 50 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{Type: "question-agent", Actions: []action.Executor{work},
		ArtifactFileStore: config.ArtifactFileStore, HeartbeatInterval: &heartbeat})
	check(err)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Serve(workerCtx, bus, nil) }()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case err := <-workerDone:
			closeError(err)
		case <-time.After(5 * time.Second):
			t.Error("question agent did not stop")
		}
	})
	adapterID, err := id.AdapterID("mcp")
	check(err)
	commands, err := controllercommands.NewClient(ctx, bus, adapterID, nil)
	check(err)
	config.Operations = commands
	t.Cleanup(func() { closeError(commands.Close()) })
	_, session := Session(t, config)
	AwaitMCPLateArtifact(t, "question agent readiness", func() bool {
		record, err := config.AgentStore.Get(ctx, worker.ID())
		return err == nil && record.Status == "ready"
	})
	return &ActionQuestion{Context: ctx, Session: session, AgentID: worker.ID(), Executions: config.ExecutionStore, Work: work}
}
