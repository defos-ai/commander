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

	"commander/framework/artifact"
	"commander/framework/controller/runner"
	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

func TestRecoveryUsesLatestOperationAfterList(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		name := "finished and resolved"
		if cancelled {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			config := fixture()
			op := seedMaintenanceTask(t, config, !cancelled)
			var latest operations.Operation
			base := config.OperationStore
			config.OperationStore = &afterListStore{OperationStore: base, afterList: func() error {
				var err error
				if cancelled {
					latest, err = base.Cancel(t.Context(), op.ID)
					return err
				}
				ref := artifact.Artifact{ID: id.ArtifactID(), Name: "result.json", AgentID: agentID, ActionName: "inspect", CommandID: op.Tasks[0].ID, CreatedAt: time.Now().UTC(),
					Files: []artifact.ArtifactFile{{ID: id.ArtifactFileID(), Name: "result.json", MediaType: "application/json", Size: 2, SHA256: strings.Repeat("a", 64)}}}
				result := actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{}`), Artifacts: []artifact.Artifact{ref}}
				latest, err = base.FinishTask(t.Context(), op.ID, operations.TaskResult{TaskID: op.Tasks[0].ID, AgentID: agentID,
					Status: operations.TaskSucceeded, Result: &result, FinishedAt: time.Now().UTC()})
				if err != nil {
					return err
				}
				latest, err = base.ResolveContinuation(t.Context(), op.ID, op.Tasks[0].ID, latest.Tasks[0].Continuation.Version, []actions.NextAction{})
				return err
			}}
			calls := make(chan invocation, 2)
			unexpected := func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				calls <- invocation{agent: agent, call: call}
				return actions.ActionOutput{}, errors.New("unexpected execution of settled work")
			}
			r := newRunner(t, config, executor{run: unexpected, recover: unexpected})
			// Submit a request so startup observation has run before closing.
			events, err := r.Events(t.Context(), op.ID, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			closeRunner(t, r)
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
			config := fixture()
			op := seedMaintenanceTask(t, config, true)
			queued := operations.NewTask(op.ID, "", "", actions.NextAction{AgentID: agentID, ActionName: "inspect"})
			if _, err := config.OperationStore.QueueTask(t.Context(), op.ID, queued); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("recovery persistence failed")
			if uncertain {
				config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, afterRecover: func(operations.Operation) error {
					return errors.Join(operations.ErrWriteUncertain, failure)
				}}
			} else {
				config.OperationStore = &recoveryFailureStore{OperationStore: config.OperationStore, failure: failure}
			}
			var executions atomic.Int32
			unexpected := func(context.Context, string, actions.ActionCall) (actions.ActionOutput, error) {
				executions.Add(1)
				return output("inspect"), nil
			}
			r, err := runner.NewRunner(t.Context(), executor{run: unexpected, recover: unexpected}, config)
			if r != nil {
				closeRunner(t, r)
			}
			if r != nil || !errors.Is(err, failure) || errors.Is(err, operations.ErrWriteUncertain) != uncertain || executions.Load() != 0 {
				t.Fatalf("startup ignored recovery failure: runner=%v, error=%v, executions=%d", r, err, executions.Load())
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
			config := fixture()
			config.MissionStore.(*missionStore).mission.RequiredActions = []string{"inspect"}
			record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: agentID, Status: agents.AgentReady,
				Actions: []actions.Action{{Name: "inspect", Status: actions.Unavailable}}}}
			catalog := &agentCatalog{}
			catalog.set(record)
			config.AgentCatalog = catalog
			failure := errors.New("preflight persistence failed")
			attempted := make(chan struct{}, 1)
			blocked := &preflightFailureStore{OperationStore: config.OperationStore, attempted: attempted, failure: failure}
			if uncertain {
				var failed atomic.Bool
				config.OperationStore = &uncertainWriteStore{OperationStore: config.OperationStore, afterPreflight: func(op operations.Operation) error {
					if op.Preflight.Ready && failed.CompareAndSwap(false, true) {
						attempted <- struct{}{}
						return errors.Join(operations.ErrWriteUncertain, failure)
					}
					return nil
				}}
			} else {
				config.OperationStore = blocked
			}
			exec, calls := controlledExecutor()
			first, err := runner.NewRunner(t.Context(), exec, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = first.Close(ctx)
			})
			op := start(t, first)
			task := assign(t, first, op.ID, "inspect")
			record.Actions[0].Status = actions.Ready
			catalog.set(record)
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
				r = newRunner(t, config, exec)
			} else {
				if eventErr != nil || stored.Preflight.Version != op.Preflight.Version || stored.Events[len(stored.Events)-1].Type != operations.TaskAssigned {
					t.Fatalf("ordinary failure published a preflight change: %+v, %v", stored, eventErr)
				}
				blocked.allowed.Store(true)
			}
			call := receive(t, calls)
			if call.agent != task.AgentID || call.call.Work.TaskID != task.ID || call.call.Work.OperationID != op.ID || !reflect.DeepEqual(call.call.Input, task.Input) {
				t.Fatalf("preflight retry replaced the saved assignment: %+v", call)
			}
			call.reply <- output("inspect")
			settled := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })
			closeRunner(t, r)
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

func seedMaintenanceTask(t *testing.T, config runner.Config, running bool) operations.Operation {
	t.Helper()
	now := time.Now().UTC()
	op := operations.Operation{ID: id.OperationID(), Mission: config.MissionStore.(*missionStore).mission.Clone(),
		Input: json.RawMessage(`{}`), Status: operations.Planning, CreatedAt: now, UpdatedAt: now}
	task := operations.NewTask(op.ID, "", "", actions.NextAction{AgentID: agentID, ActionName: "inspect", Input: json.RawMessage(`{}`)})
	if _, err := op.QueueTask(task); err != nil {
		t.Fatal(err)
	}
	if running {
		if err := op.DispatchTask(operations.TaskDispatch{TaskID: task.ID, AgentID: agentID, StartedAt: now, DeadlineAt: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.OperationStore.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op
}

type afterListStore struct {
	operations.OperationStore
	afterList func() error
}

func (s *afterListStore) List(ctx context.Context) ([]operations.Operation, error) {
	ops, err := s.OperationStore.List(ctx)
	if err == nil && s.afterList != nil {
		err = s.afterList()
		s.afterList = nil
	}
	return ops, err
}

type recoveryFailureStore struct {
	operations.OperationStore
	failure error
}

func (s *recoveryFailureStore) Recover(context.Context, string, time.Time) (operations.Operation, error) {
	return operations.Operation{}, s.failure
}

type preflightFailureStore struct {
	operations.OperationStore
	allowed   atomic.Bool
	attempted chan struct{}
	failure   error
}

func (s *preflightFailureStore) RefreshPreflight(ctx context.Context, operationID string, records []agents.Agent) (operations.Operation, error) {
	if records[0].Actions[0].Status == actions.Ready && !s.allowed.Load() {
		select {
		case s.attempted <- struct{}{}:
		default:
		}
		return operations.Operation{}, s.failure
	}
	return s.OperationStore.RefreshPreflight(ctx, operationID, records)
}
