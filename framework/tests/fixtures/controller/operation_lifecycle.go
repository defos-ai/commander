package fixtures

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	runnerfakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
)

type OwnedOperations struct {
	Context    context.Context
	Config     controller.ControllerConfig
	Bus        *fakes.Bus
	Inventory  *fakes.Inventory
	Executions *fakes.LifecycleExecutions
	ResultGate *fakes.Gate
}

func NewOwnedOperations(t *testing.T) OwnedOperations {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	bus, inventory, executions := fakes.NewBus(), &fakes.Inventory{}, &fakes.LifecycleExecutions{}
	now := time.Now().UTC()
	inventory.Set(&agents.Agent{AgentSnapshot: ObservationSnapshot(), LastHeartbeat: now, LastSeen: now, ObservedAt: now, LeaseExpiresAt: now.Add(time.Hour)})
	gate := fakes.NewGate()
	store := &fakes.DrainingOperations{OperationStore: runnerfakes.NewOperationStore()}
	store.BlockNextFinish(gate)
	mission := missions.Mission{Name: "review", Objectives: []string{"Review a document"}, RequiredActions: []string{"inspect"},
		StartingActions: []string{"inspect"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	return OwnedOperations{Context: ctx, Bus: bus, Inventory: inventory, Executions: executions, ResultGate: gate,
		Config: controller.ControllerConfig{
			AgentStore: inventory, ActionStore: fakes.UnusedActions{}, ArtifactStore: fakes.UnusedArtifacts{}, ExecutionStore: executions,
			MissionStore: runnerfakes.NewMissionStore(mission), OperationStore: store, ReportStore: runnerfakes.ReportStore{},
			HeartbeatTimeout: time.Hour, LeaseSweepInterval: time.Hour,
		}}
}

func (f OwnedOperations) Start(t *testing.T) *controller.Controller {
	t.Helper()
	c, err := controller.NewController(f.Context, f.Bus, f.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		f.ResultGate.Open()
		closing, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.Close(closing); err != nil {
			t.Error(err)
		}
	})
	return c
}

func (f OwnedOperations) SeedQueued(t *testing.T) operations.Operation {
	t.Helper()
	mission, err := f.Config.MissionStore.Get(f.Context, "review")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	op := operations.Operation{ID: id.OperationID(), Mission: mission, Input: json.RawMessage(`{}`), Status: operations.Planning, CreatedAt: now, UpdatedAt: now}
	task := operations.NewTask(op.ID, "", actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{}`)})
	if _, err := op.QueueTask(task); err != nil {
		t.Fatal(err)
	}
	if err := f.Config.OperationStore.Create(f.Context, op); err != nil {
		t.Fatal(err)
	}
	saved, err := f.Config.OperationStore.Get(f.Context, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func SuccessfulOperationResult(t *testing.T, command messages.Message) messages.Message {
	t.Helper()
	result, err := messages.NewMessage(messages.ActionSucceeded, command.AgentID, actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	result.CorrelationID, result.Work = command.ID, command.Work
	return result
}
