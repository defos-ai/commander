package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestRecoveryUsesLatestOperationAfterList(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "finished and resolved"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			config := fixtures.Config()
			op := fixtures.SeedMaintenanceTask(t, config, !cancelled)
			var latest operations.Operation
			base := config.OperationStore
			config.OperationStore = &fakes.AfterListStore{OperationStore: base, AfterList: func() error {
				var err error
				if cancelled {
					latest, err = base.Cancel(t.Context(), op.ID)
					return err
				}
				ref := artifacts.Artifact{ID: id.ArtifactID(), Name: "result.json", AgentID: fixtures.AgentID, ActionName: "inspect", CommandID: op.Tasks[0].ID, CreatedAt: time.Now().UTC(),
					Files: []artifacts.ArtifactFile{{ID: id.ArtifactFileID(), Name: "result.json", MediaType: "application/json", Size: 2, SHA256: strings.Repeat("a", 64)}}}
				result := actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{}`), Artifacts: []artifacts.Artifact{ref}}
				latest, err = base.FinishTask(t.Context(), op.ID, operations.TaskResult{TaskID: op.Tasks[0].ID, AgentID: fixtures.AgentID,
					Status: operations.TaskSucceeded, Result: &result, FinishedAt: time.Now().UTC()})
				if err != nil {
					return err
				}
				latest, err = base.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, latest.Tasks[0].Continuation.Version, []actions.NextAction{})
				return err
			}}
			calls := make(chan fakes.Invocation, 2)
			unexpected := func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				calls <- fakes.Invocation{Agent: agent, Call: call}
				return actions.ActionOutput{}, errors.New("unexpected execution of settled work")
			}
			r := fixtures.NewRunner(t, config, fakes.Executor{Run: unexpected, Recover: unexpected})
			// Submit a request so startup observation has run before closing.
			events, err := r.Events(t.Context(), op.ID, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			fixtures.CloseRunner(t, r)
			stored, err := r.Get(t.Context(), op.ID)
			if err != nil || !reflect.DeepEqual(stored.Tasks, latest.Tasks) || stored.Status != latest.Status {
				t.Fatalf("recovery replaced the latest task or status: %+v, %v", stored, err)
			}

			wantEvents := len(latest.Events)
			if !cancelled {
				wantEvents++
			}
			if len(events) != wantEvents || !reflect.DeepEqual(events[:len(latest.Events)], latest.Events) {
				t.Fatalf("recovery replaced saved events or recovered terminal work: %+v", events)
			}
			select {
			case call := <-calls:
				t.Fatalf("recovery executed work from the stale list: %+v", call)
			default:
			}
		})
	}
}

func TestRecoveryWriteFailurePreventsStartupExecution(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "ordinary failure"
		if uncertain {
			name = "uncertain write"
		}
		t.Run(name, func(t *testing.T) {
			config := fixtures.Config()
			op := fixtures.SeedMaintenanceTask(t, config, true)
			queued := operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: "inspect"})
			if _, err := config.OperationStore.QueueTask(t.Context(), op.ID, queued); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("recovery persistence failed")
			if uncertain {
				config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterRecover: func(operations.Operation) error {
					return errors.Join(operations.ErrWriteUncertain, failure)
				}}
			} else {
				config.OperationStore = &fakes.RecoveryFailureStore{OperationStore: config.OperationStore, Failure: failure}
			}
			var executions atomic.Int32
			unexpected := func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error) {
				executions.Add(1)
				return fixtures.Output("inspect"), nil
			}
			r, err := runner.NewRunner(t.Context(), fakes.Executor{Run: unexpected, Recover: unexpected}, config)
			if r != nil {
				fixtures.CloseRunner(t, r)
			}
			if r != nil || !errors.Is(err, failure) || errors.Is(err, operations.ErrWriteUncertain) != uncertain || executions.Load() != 0 {
				t.Fatalf("startup ignored recovery Failure: runner=%v, error=%v, executions=%d", r, err, executions.Load())
			}
		})
	}
}

func TestPreflightWriteFailureKeepsSavedTaskQueued(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "retry ordinary failure"
		if uncertain {
			name = "restart after uncertain write"
		}
		t.Run(name, func(t *testing.T) {
			config := fixtures.Config()
			config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) { m.RequiredActions = []string{"inspect"} })
			record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: fixtures.AgentID, Status: agents.AgentReady,
				Actions: []actions.Action{{Name: "inspect", Status: actions.Unavailable}}}}
			catalog := &fakes.AgentCatalog{}
			catalog.Set(record)
			config.AgentCatalog = catalog
			failure := errors.New("preflight persistence failed")
			attempted := make(chan struct{}, 1)
			blocked := fakes.NewPreflightFailureStore(config.OperationStore, failure, attempted)
			if uncertain {
				var failed atomic.Bool
				config.OperationStore = &fakes.UncertainWriteStore{OperationStore: config.OperationStore, AfterPreflight: func(op operations.Operation) error {
					if op.Preflight.Ready && failed.CompareAndSwap(false, true) {
						attempted <- struct{}{}
						return errors.Join(operations.ErrWriteUncertain, failure)
					}
					return nil
				}}
			} else {
				config.OperationStore = blocked
			}
			exec, calls := fakes.ControlledExecutor()
			first, err := runner.NewRunner(t.Context(), exec, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = first.Close(ctx)
			})
			op := fixtures.Start(t, first)
			task := fixtures.Assign(t, first, op.ID, "inspect")
			record.Actions[0].Status = actions.Ready
			catalog.Set(record)
			select {
			case <-attempted:
			case <-time.After(5 * time.Second):
				t.Fatal("expected preflight persistence attempt")
			}
			_, eventErr := first.Events(t.Context(), op.ID, 0, false)
			stored, err := first.Get(t.Context(), op.ID)
			if err != nil || !reflect.DeepEqual(stored.Tasks, []operations.Task{task}) || stored.Preflight.Ready != uncertain {
				t.Fatalf("failed preflight changed queued work: %+v, %v", stored, err)
			}
			select {
			case call := <-calls:
				t.Fatalf("dispatched without durable preflight: %+v", call)
			default:
			}
			r := first
			if uncertain {
				if !errors.Is(eventErr, operations.ErrWriteUncertain) || !errors.Is(eventErr, failure) {
					t.Fatalf("uncertain preflight was hidden: %v", eventErr)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if _, err := first.WaitTask(ctx, op.ID, task.ID); !errors.Is(err, operations.ErrWriteUncertain) {
					t.Fatalf("task waiter did not observe uncertainty: %v", err)
				}
				if err := first.Close(ctx); !errors.Is(err, operations.ErrWriteUncertain) {
					t.Fatalf("close did not observe uncertainty: %v", err)
				}
				r = fixtures.NewRunner(t, config, exec)
			} else {
				if eventErr != nil || stored.Preflight.Version != op.Preflight.Version || stored.Events[len(stored.Events)-1].Type != operations.TaskAssigned {
					t.Fatalf("ordinary failure published a preflight change: %+v, %v", stored, eventErr)
				}
				blocked.Allow()
			}
			call := fixtures.Receive(t, calls)
			if call.Agent != fixtures.AgentID || task.AgentID != "" || call.Call.Work.TaskID != task.ID || call.Call.Work.OperationID != op.ID || !reflect.DeepEqual(call.Call.Input, task.Input) {
				t.Fatalf("preflight retry replaced the saved assignment: %+v", call)
			}
			call.Reply <- fixtures.Output("inspect")
			settled := fixtures.AwaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
			fixtures.CloseRunner(t, r)
			counts := map[operations.EventType]int{}
			for _, event := range settled.Events {
				counts[event.Type]++
			}
			if len(settled.Tasks) != 1 || settled.Tasks[0].CommandID != task.CommandID || settled.Preflight.Version != 2 || counts[operations.PreflightUpdated] != 1 || counts[operations.TaskAssigned] != 1 || counts[operations.TaskDispatched] != 1 {
				t.Fatalf("preflight retry duplicated saved work or readiness: %+v", settled)
			}
		})
	}
}
