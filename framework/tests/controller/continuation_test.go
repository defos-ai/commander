package controller_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	agents "commander/framework/agent"
	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/comms/messages"
	"commander/framework/controller/runner"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	controllerproviders "commander/providers/controller"
)

func TestContinuationsPersistWithoutDispatchingProposals(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		count  int
		failed bool
	}{
		{"multiple proposals", 2, false},
		{"no proposals still pending", 0, false},
		{"failure needs decision", 0, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange: a real controller and runner share durable filesystem stores.
			f := newDeliveryFixture(t)
			f.markAgent(t, agents.AgentReady)
			reports, err := controllerproviders.OpenReportStorage(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reports.Close() })
			mission := missions.Mission{Name: "review", Objective: "Review", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			if err := f.database.MissionStore.Upsert(t.Context(), mission); err != nil {
				t.Fatal(err)
			}
			config := runner.Config{MissionStore: f.database.MissionStore, OperationStore: f.database.OperationStore, ReportStore: reports.ReportStore, ArtifactRestorer: f.controller}
			initial, err := runner.NewRunner(t.Context(), f.controller, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = initial.Close(context.Background()) })
			operation, err := initial.Start(t.Context(), commands.StartOperationCommand{MissionName: mission.Name, Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			task, err := initial.Assign(t.Context(), commands.AssignTaskCommand{OperationID: operation.ID, AgentID: recoveryAgentID, ActionName: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			command := f.nextCommand(t)
			actions := make([]actionmodel.NextAction, scenario.count)
			for i := range actions {
				actions[i] = actionmodel.NextAction{ActionName: "scan-ports", Input: json.RawMessage(`{"target":"192.0.2.1"}`), Reason: "discovery"}
			}
			result := recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{ActionName: "fixture", Output: json.RawMessage(`{}`), NextActions: actions})
			if scenario.failed {
				result = recoveryReply(t, command, messages.CommandFailed, agents.CommandError{ErrorType: "action_error", Message: "failed"})
			}

			// Act: accept a result and replay it, then restart both controller and runner.
			f.publish(t, result)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			finished, err := initial.WaitTask(ctx, operation.ID, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.nextReceipt(t)
			f.publish(t, result)
			f.nextReceipt(t)
			before, err := initial.Get(t.Context(), operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := initial.Close(ctx); err != nil {
				t.Fatal(err)
			}
			f.restart(t)
			config.ArtifactRestorer = f.controller
			restarted, err := runner.NewRunner(t.Context(), f.controller, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restarted.Close(context.Background()) })
			after, err := restarted.Get(t.Context(), operation.ID)
			if err != nil {
				t.Fatal(err)
			}

			// Assert: proposals survive unchanged; only the original command and two receipts were published.
			if len(after.Tasks) != 1 || f.bus.published.Load() != 3 || !reflect.DeepEqual(before.Tasks, after.Tasks) {
				t.Fatalf("replay or recovery changed tasks: before=%+v after=%+v publishes=%d", before, after, f.bus.published.Load())
			}
			continuation := after.Tasks[0].Continuation
			if continuation == nil || continuation.Status != operations.ContinuationPending || len(continuation.ProposedActions) != scenario.count || continuation.ProposedActions == nil || len(continuation.CreatedTaskIDs) != 0 {
				t.Fatalf("unexpected continuation: %+v", continuation)
			}
			if scenario.count > 0 {
				// Public snapshots must not share proposal buffers with the runner.
				finished.Continuation.ProposedActions[0].Input[0] = '['
				copy := after.Clone()
				copy.Tasks[0].Continuation.ProposedActions[0].Input[0] = '['
				if !json.Valid(after.Tasks[0].Continuation.ProposedActions[0].Input) || !json.Valid(after.Tasks[0].Result.NextActions[0].Input) {
					t.Fatal("proposal buffers alias")
				}
			}
		})
	}
}
