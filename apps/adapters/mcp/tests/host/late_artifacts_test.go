package mcp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	adapter "github.com/defos-ai/commander/apps/adapters/mcp"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/packages"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	adapterconsumer "github.com/defos-ai/commander/framework/controller/consumers/adapter"
	"github.com/defos-ai/commander/framework/controller/runner"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
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

func TestMCPLateArtifactRemainsReadableWithoutAnotherExecution(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "active operation"
		if completed {
			name = "completed operation"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newMCPLateArtifact(t)
			var op operations.Operation
			fixture.call(t, "start_operation", map[string]any{"mission_name": "late-artifact", "input": map[string]any{}}, &op)
			var task operations.Task
			fixture.call(t, "assign_task", map[string]any{"operation_id": op.ID, "action_name": "late-artifact", "input": map[string]any{}}, &task)
			select {
			case <-fixture.started:
			case <-time.After(5 * time.Second):
				t.Fatal("assigned action did not start before its deadline")
			}
			arguments := map[string]any{"operation_id": op.ID, "task_id": task.ID}
			var interrupted operations.Task
			awaitMCPLateArtifact(t, "interrupted task", func() bool {
				fixture.call(t, "get_task", arguments, &interrupted)
				return interrupted.Status == operations.TaskInterrupted
			})
			if interrupted.Result != nil || interrupted.AgentID != fixture.agentID {
				t.Fatalf("unexpected interrupted execution: %+v", interrupted)
			}
			var savedReport reports.Report
			if completed {
				fixture.call(t, "complete_operation", map[string]any{"operation_id": op.ID,
					"output": map[string]any{"limitations": []string{"Execution outcome is unknown"}}}, &op)
				fixture.call(t, "get_report", map[string]any{"operation_id": op.ID}, &savedReport)
			}
			fixture.release()
			var finished operations.Task
			awaitMCPLateArtifact(t, "late task result", func() bool {
				fixture.call(t, "get_task", arguments, &finished)
				return finished.Status == operations.TaskSucceeded
			})
			if finished.ID != task.ID || finished.CommandID != task.CommandID || finished.AgentID != fixture.agentID || finished.Result == nil || len(finished.Result.Artifacts) != 1 {
				t.Fatalf("late result changed execution identity or lost evidence: %+v", finished)
			}
			ref := finished.Result.Artifacts[0]
			fixture.assertResource(t, ref)
			saved, err := fixture.executions.Get(fixture.ctx, fixture.agentID, task.CommandID)
			if err != nil || saved.Result == nil {
				t.Fatalf("accepted execution result is missing: %v", err)
			}
			var accepted actionmodel.ActionOutput
			if err := json.Unmarshal(saved.Result.Payload, &accepted); err != nil || len(accepted.Artifacts) != 1 || !accepted.Artifacts[0].Equal(ref) {
				t.Fatalf("MCP changed the accepted artifact or file identity: %+v, %v", accepted, err)
			}
			var refreshed struct {
				operations.Operation
				Progress struct {
					FinishedTasks int  `json:"finished_tasks"`
					Settled       bool `json:"settled"`
				} `json:"progress"`
			}
			fixture.call(t, "get_operation", map[string]any{"operation_id": op.ID}, &refreshed)
			if len(refreshed.Tasks) != 1 || !reflect.DeepEqual(refreshed.Tasks[0].Result, finished.Result) || refreshed.Progress.FinishedTasks != 1 {
				t.Fatalf("operation detail lost its late task artifact: %+v", refreshed)
			}
			if completed {
				var report reports.Report
				fixture.call(t, "get_report", map[string]any{"operation_id": op.ID}, &report)
				if refreshed.Status != operations.Completed || !refreshed.Progress.Settled || refreshed.ReportID != op.ReportID || !reflect.DeepEqual(report, savedReport) {
					t.Fatalf("late evidence changed finalization or report: %+v", refreshed)
				}
			} else {
				if refreshed.Status != operations.Planning || refreshed.Progress.Settled {
					t.Fatalf("successful task unexpectedly completed its operation: %+v", refreshed)
				}
				fixture.call(t, "cancel_operation", map[string]any{"operation_id": op.ID}, &op)
			}
			if fixture.invocations.Load() != 1 {
				t.Fatalf("late recovery executed action %d times", fixture.invocations.Load())
			}
		})
	}
}

type mcpLateArtifact struct {
	ctx         context.Context
	session     *sdkmcp.ClientSession
	agentID     string
	executions  executions.ExecutionStore
	started     chan struct{}
	release     func()
	invocations atomic.Int32
}

func newMCPLateArtifact(t *testing.T) *mcpLateArtifact {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	fixture := &mcpLateArtifact{ctx: ctx, started: make(chan struct{}, 1)}
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
	fixture.executions = executions
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
	gate := make(chan struct{})
	var release sync.Once
	fixture.release = func() { release.Do(func() { close(gate) }) }
	workerBus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	check(err)
	t.Cleanup(func() { cleanupError(workerBus.Close()) })
	heartbeat := 50 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{Type: "late-artifact-agent",
		Actions: []action.Executor{&mcpLateArtifactAction{Base: action.NewBase(actionmodel.Action{Name: "late-artifact",
			InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}), fixture: fixture, gate: gate}},
		ArtifactFileStore: config.ArtifactFileStore, HeartbeatInterval: &heartbeat,
	})
	check(err)
	fixture.agentID = worker.ID()
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
	t.Cleanup(fixture.release)
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
	fixture.session, err = sdkmcp.NewClient(&sdkmcp.Implementation{Name: "late-artifact-test", Version: "1"}, nil).Connect(ctx, clientTransport, nil)
	check(err)
	t.Cleanup(func() { cleanupError(fixture.session.Close()) })
	awaitMCPLateArtifact(t, "ready agent", func() bool {
		record, err := config.AgentStore.Get(ctx, worker.ID())
		return err == nil && record.Status == "ready"
	})
	return fixture
}

type mcpLateArtifactAction struct {
	*action.Base
	fixture *mcpLateArtifact
	gate    <-chan struct{}
}

func (a *mcpLateArtifactAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	a.fixture.invocations.Add(1)
	select {
	case a.fixture.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return action.Result{}, ctx.Err()
	case <-a.gate:
	}
	return action.Result{Output: json.RawMessage(`{"built":true}`), Artifacts: []action.Artifact{{Name: "Late source",
		Files: []artifacts.FileSource{{Name: "source.txt", MediaType: "text/plain", Data: []byte("late source evidence\n")}},
	}}}, nil
}

func (f *mcpLateArtifact) call(t *testing.T, name string, input, output any) {
	t.Helper()
	missionCall(t, f.ctx, f.session, name, input, output)
}

func (f *mcpLateArtifact) assertResource(t *testing.T, ref artifacts.Artifact) {
	t.Helper()
	uri := "missions://artifacts/" + ref.ID
	resource, err := f.session.ReadResource(f.ctx, &sdkmcp.ReadResourceParams{URI: uri})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("late artifact metadata unreadable: %v", err)
	}
	var metadata artifacts.Artifact
	if err := json.Unmarshal([]byte(resource.Contents[0].Text), &metadata); err != nil || !metadata.Equal(ref) || len(metadata.Files) != 1 {
		t.Fatalf("late artifact metadata differs from task result: %+v, %v", metadata, err)
	}
	file := metadata.Files[0]
	resource, err = f.session.ReadResource(f.ctx, &sdkmcp.ReadResourceParams{URI: uri + "/files/" + file.ID})
	if err != nil || len(resource.Contents) != 1 {
		t.Fatalf("late artifact bytes unreadable: %v", err)
	}
	data := []byte(resource.Contents[0].Text)
	digest := sha256.Sum256(data)
	if !bytes.Equal(data, []byte("late source evidence\n")) || int64(len(data)) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
		t.Fatal("late artifact download lost its original bytes or checksum")
	}
}

func awaitMCPLateArtifact(t *testing.T, name string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
