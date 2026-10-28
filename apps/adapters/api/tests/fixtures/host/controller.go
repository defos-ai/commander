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

	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	adapterconsumer "github.com/defos-ai/commander/framework/controller/consumers/adapter"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
	fsmissions "github.com/defos-ai/commander/providers/fs/missions"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
	fsreports "github.com/defos-ai/commander/providers/fs/reports"
)

func StartLateArtifactAPI(t *testing.T, propose bool) (*API, *agent.Agent, *fakes.LateArtifactAction, executions.ExecutionStore) {
	t.Helper()
	fixture := New(t)
	database := filepath.Join(fixture.Root, "database")
	config := controller.ControllerConfig{}
	var err error
	if config.AgentStore, err = fsagents.NewAgentFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ActionStore, err = fsactions.NewActionFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ArtifactStore, err = fsartifacts.NewArtifactFsStore(database); err != nil {
		t.Fatal(err)
	}
	if config.ExecutionStore, err = fsexecutions.NewExecutionFsStore(database); err != nil {
		t.Fatal(err)
	}
	bus := NewSecretTestBus(t, fixture.Root)
	coordinator, err := controller.NewController(t.Context(), bus, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseSecretController(t, coordinator) })
	startOperationRunnerWithTimeout(t, coordinator, bus, database, time.Second, missions.Mission{
		Name: "late-artifact", Objectives: []string{"Retain the original execution evidence"},
		RequiredActions: []string{"late-artifact"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	})
	files, err := fsartifacts.NewArtifactFileFsStore(filepath.Join(fixture.Root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	work := fakes.NewLateArtifactAction(LateArtifactContents, propose)
	heartbeat := 100 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{Type: "artifact-agent", Actions: []action.Executor{work},
		ArtifactFileStore: files, HeartbeatInterval: &heartbeat})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	agentBus := NewSecretTestBus(t, fixture.Root)
	go func() { done <- worker.Serve(ctx, agentBus, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("artifact agent shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("artifact agent shutdown timed out")
		}
	})
	t.Cleanup(work.Unblock)
	server := Start(t, fixture.LocalArgs())
	AwaitSecretCondition(t, "artifact action registration", func() bool {
		registered, err := config.AgentStore.Get(t.Context(), worker.ID())
		return err == nil && registered.Status == "ready"
	})
	return server, worker, work, config.ExecutionStore
}

const LateArtifactContents = "source archive from the original execution\n"

func StartOperationRunner(t *testing.T, coordinator *controller.Controller, bus comms.Bus, database string, definitions ...missions.Mission) func() {
	t.Helper()
	return startOperationRunnerWithTimeout(t, coordinator, bus, database, 0, definitions...)
}

func startOperationRunnerWithTimeout(t *testing.T, coordinator *controller.Controller, bus comms.Bus, database string, timeout time.Duration, definitions ...missions.Mission) func() {
	t.Helper()
	missionStore, err := fsmissions.NewMissionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if err := missionStore.Upsert(t.Context(), definition); err != nil {
			t.Fatal(err)
		}
	}
	operationStore, err := fsoperations.NewOperationFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	reportStore, err := fsreports.NewReportFsStore(filepath.Join(filepath.Dir(database), "storage"))
	if err != nil {
		t.Fatal(err)
	}
	execution, err := runner.NewRunner(t.Context(), coordinator, runner.Config{
		MissionStore: missionStore, OperationStore: operationStore, ReportStore: reportStore, AgentCatalog: coordinator,
		TaskTimeout: timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	commandCtx, cancelCommands := context.WithCancel(t.Context())
	var commands *adapterconsumer.Consumer
	var done chan error
	stopped := false
	stop := func() {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		// Stop the reader before closing subscriptions, including in-flight ACKs.
		cancelCommands()
		if commands != nil {
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("operation command consumer: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("operation command consumer shutdown timed out")
			}
			if err := commands.Close(); err != nil {
				t.Errorf("operation command consumer shutdown: %v", err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := execution.Close(ctx); err != nil {
			t.Errorf("operation runner shutdown: %v", err)
		}
	}
	t.Cleanup(stop)
	commands, err = adapterconsumer.NewConsumer(t.Context(), bus, messages.DefaultControllerType, coordinator.ID(), coordinator, execution, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	done = make(chan error, 1)
	go func() { done <- commands.Consume(commandCtx) }()
	return stop
}

func NewSecretTestBus(t *testing.T, root string) *fscomms.FsBus {
	t.Helper()
	bus, err := fscomms.NewFsBus(filepath.Join(root, "bus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	return bus
}

func CloseSecretController(t *testing.T, runtime *controller.Controller) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		t.Errorf("controller shutdown: %v", err)
	}
}

func SecretActionStatus(worker *agent.Agent, name string) action.Status {
	for _, definition := range worker.Describe().Actions {
		if definition.Name == name {
			return definition.Status
		}
	}
	return action.Unknown
}

func AwaitSecretCondition(t *testing.T, description string, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", description)
		case <-ticker.C:
		}
	}
}

func AwaitSecretInstallation(t *testing.T, requests secretmodel.RequestStore, deliveries <-chan messages.Message, secretID string, version uint64) {
	t.Helper()
	var requestID string
	AwaitSecretCondition(t, "matching installed secret version", func() bool {
		for {
			select {
			case message := <-deliveries:
				payload, err := agentevents.DecodeSecretDelivery(message)
				if err == nil && payload.Secret.Metadata.ID == secretID && payload.Secret.Metadata.Version == version {
					requestID = message.CorrelationID
				}
			default:
				if requestID == "" {
					return false
				}
				record, err := requests.Get(t.Context(), requestID)
				return err == nil && record.Request.Status == secretmodel.Fulfilled
			}
		}
	})
}
