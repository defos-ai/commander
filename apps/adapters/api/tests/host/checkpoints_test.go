package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/agent"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	fsactions "github.com/defos-ai/commander/providers/fs/actions"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fsartifacts "github.com/defos-ai/commander/providers/fs/artifacts"
	fsexecutions "github.com/defos-ai/commander/providers/fs/executions"
)

// The agent has its own bus connection and no execution store. Its HTTP-assigned
// action must await controller persistence and acknowledgement before proceeding.
func TestActionCheckpointsThroughAPIRecoverLostAcknowledgement(t *testing.T) {
	fixture := newFixture(t)
	database := filepath.Join(fixture.root, "database")
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
	observedBus := &checkpointDroppingBus{
		Bus:     newSecretTestBus(t, fixture.root),
		dropped: make(chan struct{}, 1), release: make(chan struct{}),
	}
	coordinator, err := controller.NewController(t.Context(), observedBus, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeSecretController(t, coordinator) })
	stopOperations := startOperationRunner(t, coordinator, observedBus, database, missions.Mission{
		Name: "checkpoint-execution", Objective: "Confirm a durable intent before performing work",
		RequiredActions: []string{"checkpoint-action"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	})
	files, err := fsartifacts.NewArtifactFileFsStore(filepath.Join(fixture.root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	intent := json.RawMessage(`{"resource":"fixture-resource"}`)
	var effects atomic.Int32
	type output struct {
		Confirmed bool `json:"confirmed"`
	}
	executor, err := action.NewFunc(action.FuncConfig[struct{}, output]{
		Action: actionmodel.Action{Name: "checkpoint-action"},
		Run: func(ctx context.Context, _ struct{}) (output, error) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if data, found, err := action.LoadCheckpoint(ctx, "intent"); err != nil || found || len(data) != 0 {
				return output{}, fmt.Errorf("fresh execution checkpoint: found=%t err=%v", found, err)
			}
			observedBus.armed.Store(true)
			if err := action.SaveCheckpoint(ctx, "intent", intent); err != nil {
				return output{}, err
			}
			effects.Add(1)
			data, found, err := action.LoadCheckpoint(ctx, "intent")
			if err != nil {
				return output{}, err
			}
			return output{Confirmed: found && bytes.Equal(data, intent)}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	heartbeat := 100 * time.Millisecond
	worker, err := agent.NewAgent(agent.AgentConfig{
		Type: "checkpoint-agent", Actions: []action.Executor{executor},
		ArtifactFileStore: files, HeartbeatInterval: &heartbeat,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentBus := newSecretTestBus(t, fixture.root)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Serve(ctx, agentBus, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("agent shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent shutdown timed out")
		}
	})
	// Release the fault gate even when an assertion fails before the normal ack.
	var release sync.Once
	unblock := func() { release.Do(func() { close(observedBus.release) }) }
	t.Cleanup(unblock)
	server := startAPI(t, fixture.localArgs())
	awaitSecretCondition(t, "checkpoint action registration", func() bool {
		registered, err := config.AgentStore.Get(t.Context(), worker.ID())
		return err == nil && registered.Status == "ready"
	})
	operation := decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, "/api/v1/operations",
		`{"mission_name":"checkpoint-execution","input":{}}`), http.StatusCreated)
	operationPath := "/api/v1/operations/" + operation.ID
	queued := decodeResponse[models.TaskResponse](t, secretRequest(t, server, http.MethodPost, operationPath+"/tasks",
		`{"action_name":"checkpoint-action","input":{}}`), http.StatusCreated)
	select {
	case <-observedBus.dropped:
	case <-time.After(5 * time.Second):
		t.Fatal("controller did not acknowledge the checkpoint")
	}
	saved, err := config.ExecutionStore.Get(t.Context(), worker.ID(), queued.CommandID)
	if err != nil || !bytes.Equal(saved.Checkpoints["intent"], intent) || saved.Result != nil {
		t.Fatalf("acknowledgement preceded durable checkpoint: execution=%+v err=%v", saved, err)
	}
	if effects.Load() != 0 {
		t.Fatal("action continued without receiving the persistence acknowledgement")
	}
	unblock()
	var completed models.OperationResponse
	awaitSecretCondition(t, "checkpointed action completion after acknowledgement retry", func() bool {
		completed = decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodGet, operationPath, ""))
		return len(completed.Tasks) == 1 && completed.Tasks[0].Status == "succeeded"
	})
	task := completed.Tasks[0]
	var result output
	if task.ID != queued.ID || task.CommandID != queued.CommandID || task.AgentID != worker.ID() ||
		task.Result == nil || json.Unmarshal(task.Result.Output, &result) != nil || !result.Confirmed || effects.Load() != 1 {
		t.Fatalf("checkpoint retry did not preserve the original action outcome: task=%+v effects=%d", task, effects.Load())
	}
	// Reopen the provider so the final outcome and intermediate fact cannot be
	// supplied by the action's or controller's process memory.
	reopened, err := fsexecutions.NewExecutionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	saved, err = reopened.Get(t.Context(), worker.ID(), task.CommandID)
	if err != nil || saved.Result == nil || len(saved.Checkpoints) != 1 || !bytes.Equal(saved.Checkpoints["intent"], intent) {
		t.Fatalf("completed execution lost its checkpoint or result: execution=%+v err=%v", saved, err)
	}
	decodeResponse[models.OperationResponse](t, secretRequest(t, server, http.MethodPost, operationPath+"/cancel", ""))
	stopOperations()
}

type checkpointDroppingBus struct {
	comms.Bus
	armed   atomic.Bool
	lost    atomic.Bool
	dropped chan struct{}
	release chan struct{}
}

func (bus *checkpointDroppingBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if message.Type == agentevents.CheckpointResponded && bus.armed.Load() {
		if !bus.lost.Swap(true) {
			bus.dropped <- struct{}{}
			return nil
		}
		select {
		case <-bus.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return bus.Bus.Publish(ctx, topic, message)
}
