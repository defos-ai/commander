package runner_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agents "commander/framework/agent"
	"commander/framework/artifact"
	commands "commander/framework/comms/clients/controller/commands"
	"commander/framework/controller/runner"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	provider "commander/providers/controller"
)

// A remote action continues independently of the controller's wait. This executor
// models an outcome that stays unknown until the observation deadline expires.
type waitingExecutor struct {
	started    chan struct{}
	dispatches atomic.Int32
	recoveries atomic.Int32
}

func (e *waitingExecutor) RunAction(ctx context.Context, _ string, _ agents.ActionCall) (agents.ActionOutput, error) {
	e.dispatches.Add(1)
	close(e.started)
	<-ctx.Done()
	return agents.ActionOutput{}, ctx.Err()
}

func (e *waitingExecutor) RecoverAction(ctx context.Context, _ string, _ agents.ActionCall) (agents.ActionOutput, error) {
	e.recoveries.Add(1)
	<-ctx.Done()
	return agents.ActionOutput{}, ctx.Err()
}

type artifactIndex struct{}

func (artifactIndex) RestoreArtifacts([]artifact.Ref) error { return nil }

func TestRecoveryRejectsTasksWithoutExecutionIdentityOrDeadline(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*operations.Task)
		wantErr string
	}{
		{
			name: "command ID",
			corrupt: func(task *operations.Task) {
				task.CommandID = ""
			},
			wantErr: "has a mismatched command identity",
		},
		{
			name: "deadline",
			corrupt: func(task *operations.Task) {
				task.DeadlineAt = nil
			},
			wantErr: "has no execution deadline",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: incomplete persisted tasks must not be rewritten or dispatched.
			database, err := provider.OpenDatabase(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })

			storage, err := provider.OpenReportStorage(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = storage.Close() })

			now := time.Now().UTC()
			deadline := now.Add(time.Minute)
			operationID, taskID := id.OperationID(), id.TaskID()

			task := operations.Task{
				ID:          taskID,
				CommandID:   taskID,
				OperationID: operationID,
				AgentID:     "fixture-agent-1234abcd",
				ActionName:  "wait",
				Status:      operations.TaskRunning,
				StartedAt:   now,
				DeadlineAt:  &deadline,
			}
			test.corrupt(&task)

			operation := operations.Operation{
				ID:        operationID,
				Version:   1,
				Status:    operations.Running,
				CreatedAt: now,
				UpdatedAt: now,
				Tasks:     []operations.Task{task},
			}

			if err := database.OperationStore.Create(t.Context(), operation); err != nil {
				t.Fatal(err)
			}

			// Read the canonical persisted representation before recovery.
			before, err := database.OperationStore.Get(t.Context(), operationID)
			if err != nil {
				t.Fatal(err)
			}

			executor := &waitingExecutor{started: make(chan struct{})}
			config := runner.Config{
				MissionStore:     database.MissionStore,
				OperationStore:   database.OperationStore,
				ReportStore:      storage.ReportStore,
				ArtifactRestorer: artifactIndex{},
			}

			// Act.
			restarted, recoveryErr := runner.NewRunner(t.Context(), executor, config)
			if restarted != nil {
				defer restarted.Close(context.Background())
			}

			after, readErr := database.OperationStore.Get(t.Context(), operationID)

			// Assert.
			if restarted != nil {
				t.Fatal("recovery accepted an incomplete execution record")
			}
			if recoveryErr == nil {
				t.Fatal("recovery accepted an incomplete execution record")
			}
			if !strings.Contains(recoveryErr.Error(), test.wantErr) {
				t.Fatalf("unexpected recovery error: %v; want %q", recoveryErr, test.wantErr)
			}
			if readErr != nil {
				t.Fatalf("read persisted operation: %v", readErr)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid task changed persisted operation:\nbefore=%+v\nafter=%+v", before, after)
			}
			if dispatches := executor.dispatches.Load(); dispatches != 0 {
				t.Fatalf("invalid task caused %d dispatches", dispatches)
			}
			if recoveries := executor.recoveries.Load(); recoveries != 0 {
				t.Fatalf("invalid task caused %d recoveries", recoveries)
			}
		})
	}
}

func TestRecoveryKeepsOriginalTaskDeadlineWhenHostConfigurationChanges(t *testing.T) {
	// Arrange: dispatch a task through the public runner, then stop observation
	// before its outcome is known. Real filesystem stores survive both runners.
	database, err := provider.OpenDatabase(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storage, err := provider.OpenReportStorage(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	mission := missions.Mission{Name: "wait-for-result", Objective: "Observe remote work", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}
	if err := database.MissionStore.Upsert(t.Context(), mission); err != nil {
		t.Fatal(err)
	}
	config := runner.Config{MissionStore: database.MissionStore, OperationStore: database.OperationStore,
		ReportStore: storage.ReportStore, ArtifactRestorer: artifactIndex{}, TaskTimeout: time.Second}
	executor := &waitingExecutor{started: make(chan struct{})}
	initial, err := runner.NewRunner(t.Context(), executor, config)
	if err != nil {
		t.Fatal(err)
	}
	closeRunner := func(value *runner.Runner) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := value.Close(ctx); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { closeRunner(initial) })
	operation, err := initial.Start(t.Context(), commands.StartOperationCommand{MissionName: mission.Name, Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	task, err := initial.Assign(t.Context(), commands.AssignTaskCommand{OperationID: operation.ID, AgentID: "fixture-agent-03b7d4e3", ActionName: "wait", Input: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.started:
	case <-time.After(3 * time.Second):
		t.Fatal("action was not dispatched")
	}
	before, err := initial.Get(t.Context(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	task = before.Tasks[0]
	closeRunner(initial)
	config.TaskTimeout = time.Hour

	// Act: replace the runner with a much longer default timeout. Its existing
	// task still resolves within the original deadline (or bounded final check).
	restarted, err := runner.NewRunner(t.Context(), executor, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeRunner(restarted) })
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	result, err := restarted.WaitTask(ctx, operation.ID, task.ID)

	// Assert: changing host configuration never extends an existing assignment
	// to an hour, and observing it again never dispatches a second command.
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != operations.TaskInterrupted || result.Result != nil || result.Error == "" {
		t.Fatalf("unresolved task = %+v", result)
	}
	if task.DeadlineAt == nil || result.DeadlineAt == nil || !task.DeadlineAt.Equal(*result.DeadlineAt) {
		t.Fatalf("task deadline changed: before=%v after=%v", task.DeadlineAt, result.DeadlineAt)
	}
	if executor.dispatches.Load() != 1 || executor.recoveries.Load() != 1 {
		t.Fatalf("dispatches=%d recoveries=%d", executor.dispatches.Load(), executor.recoveries.Load())
	}
}
