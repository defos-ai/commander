package fixtures

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
)

const AgentID = "review-agent-00000001"

func Config() runner.Config {
	catalog := &fakes.AgentCatalog{}
	catalog.Set(ReadyAgent(AgentID))
	return runner.Config{
		AgentCatalog:   catalog,
		MissionStore:   fakes.NewMissionStore(missions.Mission{Name: "review", Objectives: []string{"Review a document"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}),
		OperationStore: fakes.NewOperationStore(),
		ReportStore:    fakes.ReportStore{},
	}
}
func NewRunner(t *testing.T, config runner.Config, e fakes.Executor) *runner.Runner {
	t.Helper()
	r, err := runner.NewRunner(t.Context(), e, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { CloseRunner(t, r) })
	return r
}
func CloseRunner(t *testing.T, r *runner.Runner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Errorf("close runner: %v", err)
	}
}
func Start(t *testing.T, r *runner.Runner) operations.Operation {
	t.Helper()
	op, err := r.Start(t.Context(), commands.StartOperationCommand{MissionName: "review", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}
func Assign(t *testing.T, r *runner.Runner, opID, action string) operations.Task {
	t.Helper()
	task, err := r.Assign(t.Context(), commands.AssignTaskCommand{OperationID: opID, ActionName: action, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return task
}
func Receive(t *testing.T, calls <-chan fakes.Invocation) fakes.Invocation {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("expected action dispatch")
		return fakes.Invocation{}
	}
}
func AwaitOperation(t *testing.T, r *runner.Runner, id string, ready func(operations.Operation) bool) operations.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := r.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ready(op) {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not settle: %+v", op)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func Output(name string, next ...actions.NextAction) actions.ActionOutput {
	return actions.ActionOutput{ActionName: name, Output: json.RawMessage(`{"ok":true}`), NextActions: next}
}

func ReadyAgent(id string) agents.Agent {
	return agents.Agent{AgentSnapshot: agents.AgentSnapshot{
		ID: id, Type: "review", Status: agents.AgentReady,
		Actions: []actions.Action{
			{Name: "inspect", Status: actions.Ready},
			{Name: "discover", Status: actions.Ready},
			{Name: "follow-up", Status: actions.Ready},
		},
	}}
}
