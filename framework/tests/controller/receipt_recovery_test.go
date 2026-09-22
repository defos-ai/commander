package controller_test

import (
	"context"
	"encoding/json"
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

func TestRestartReconcilesInterruptedTaskAfterReceiptFailure(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "cancelled operation", "completed operation", "continuation"} {
		t.Run(scenario, func(t *testing.T) {
			// Arrange: real controller, runner and stores; only receipt publication fails.
			f := newDeliveryFixture(t)
			f.markAgent(t, agents.AgentReady)
			f.bus.failReceipts.Store(true)
			reports, err := controllerproviders.OpenReportStorage(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reports.Close() })
			mission := missions.Mission{Name: "review", Objective: "Review", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
			if err := f.database.MissionStore.Upsert(t.Context(), mission); err != nil {
				t.Fatal(err)
			}
			config := runner.Config{MissionStore: f.database.MissionStore, OperationStore: f.database.OperationStore,
				ReportStore: reports.ReportStore, ArtifactRestorer: f.controller}
			initial, err := runner.NewRunner(t.Context(), f.controller, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = initial.Close(context.Background()) })
			var actions []actionmodel.NextAction
			if scenario == "continuation" {
				actions = []actionmodel.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`{}`)}}
			}
			operation, err := initial.Start(t.Context(), commands.StartOperationCommand{MissionName: mission.Name, Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			task, err := initial.Assign(t.Context(), commands.AssignTaskCommand{OperationID: operation.ID, AgentID: recoveryAgentID, ActionName: "fixture", Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			command := f.nextCommand(t)
			result := recoveryReply(t, command, messages.ActionSucceeded, agents.ActionOutput{ActionName: "fixture", Output: json.RawMessage(`{"ok":true}`), NextActions: actions})
			wantTaskStatus := operations.TaskSucceeded
			if scenario == "failure" {
				result = recoveryReply(t, command, messages.CommandFailed, agents.CommandError{ErrorType: "action_error", Message: "saved action failure"})
				wantTaskStatus = operations.TaskFailed
			}
			f.publish(t, result)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			interrupted, err := initial.WaitTask(ctx, operation.ID, task.ID)
			if err != nil || interrupted.Status != operations.TaskInterrupted {
				t.Fatalf("expected interruption after failed receipt: %+v, %v", interrupted, err)
			}
			saved, err := f.journal.Get(t.Context(), recoveryAgentID, command.ID)
			if err != nil || saved.Result == nil || f.bus.wasAcked(result.ID) {
				t.Fatalf("expected durable outcome with unacknowledged delivery: %+v, %v", saved, err)
			}
			wantOperationStatus := operations.Planning
			if scenario == "cancelled operation" {
				_, err = initial.Cancel(t.Context(), commands.CancelOperationCommand{OperationID: operation.ID})
				wantOperationStatus = operations.Cancelled
			} else if scenario == "completed operation" {
				_, err = initial.Complete(t.Context(), commands.CompleteOperationCommand{OperationID: operation.ID, Output: json.RawMessage(`{}`)})
				wantOperationStatus = operations.Completed
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := initial.Get(t.Context(), operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := initial.Close(ctx); err != nil {
				t.Fatal(err)
			}

			// Act: restart with the original stores, without an agent replaying its result.
			f.restart(t)
			config.ArtifactRestorer = f.controller
			restarted, err := runner.NewRunner(t.Context(), f.controller, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restarted.Close(context.Background()) })
			deadline := time.NewTimer(3 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			var recovered operations.Operation
			for {
				recovered, err = restarted.Get(t.Context(), operation.ID)
				if err != nil {
					t.Fatal(err)
				}
				if recovered.Tasks[0].Status == wantTaskStatus {
					break
				}
				select {
				case <-ticker.C:
				case <-deadline.C:
					t.Fatalf("task not reconciled: %+v", recovered.Tasks[0])
				}
			}

			// Assert: recover the saved outcome, preserve terminal state/report, never redispatch.
			if recovered.Status != wantOperationStatus || recovered.ReportID != before.ReportID || f.bus.published.Load() != 2 {
				t.Fatalf("recovery changed operation or dispatched work: %+v, publishes=%d", recovered, f.bus.published.Load())
			}
			recoveredTask := recovered.Tasks[0]
			if scenario == "continuation" {
				if interrupted.Continuation == nil || len(interrupted.Continuation.ProposedActions) != 0 {
					t.Fatalf("unknown outcome should await proposals: %+v", interrupted.Continuation)
				}
				continuation := recoveredTask.Continuation
				if continuation == nil || continuation.Status != operations.ContinuationPending || len(continuation.ProposedActions) != 1 || continuation.ProposedActions[0].ActionName != "follow-up" {
					t.Fatalf("saved proposals not reconciled: %+v", continuation)
				}
			}
			if wantTaskStatus == operations.TaskSucceeded {
				if recoveredTask.Result == nil || recoveredTask.Error != "" {
					t.Fatalf("saved success not restored: %+v", recoveredTask)
				}
				var output struct {
					OK bool `json:"ok"`
				}
				if err := json.Unmarshal(recoveredTask.Result.Output, &output); err != nil || !output.OK {
					t.Fatalf("saved output not restored: %s, %v", recoveredTask.Result.Output, err)
				}
			} else if recoveredTask.Result != nil || recoveredTask.Error == "" || recoveredTask.Error == interrupted.Error {
				t.Fatalf("saved failure not restored: %+v", recoveredTask)
			}
		})
	}
}
