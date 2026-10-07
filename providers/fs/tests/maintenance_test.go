package providers_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func TestOperationPreflightUsesLatestContentWithoutRewritingNoop(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op := createMaintenanceOperation(t, first)
	records := maintenanceInventory()
	published, err := first.RefreshPreflight(t.Context(), op.ID, records)
	if err != nil || published.Preflight == nil || !published.Preflight.Ready || published.Preflight.Version != 1 {
		t.Fatalf("initial preflight was not published: %+v, %v", published, err)
	}
	latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Preflight.Version = 41
		next.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionAnswered, Answer: "Keep this answer"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "operations", op.ID+".json")
	before := readDispatchFile(t, path)
	unchanged, err := first.RefreshPreflight(t.Context(), op.ID, records)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, unchanged, latest)
	if !bytes.Equal(before, readDispatchFile(t, path)) {
		t.Fatal("identical observations changed bytes, timestamp, events, or storage revision")
	}
	records[0].Stale = true
	started := time.Now().UTC()
	changed, err := first.RefreshPreflight(t.Context(), op.ID, records)
	if err != nil || changed.Preflight.Ready || changed.Preflight.Version != 42 || changed.Status != operations.Waiting {
		t.Fatalf("changed observations did not advance latest preflight: %+v, %v", changed, err)
	}
	expected := latest.Clone()
	expected.Preflight, expected.Status = changed.Preflight, operations.Waiting
	assertTerminalSnapshot(t, expected, changed, operations.PreflightUpdated, started)
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, changed)
}

func TestOperationConcurrentIdenticalPreflightPublishesOneEvent(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op := createMaintenanceOperation(t, first)
	type result struct {
		op  operations.Operation
		err error
	}
	gate, results := make(chan struct{}), make(chan result, 2)
	for _, store := range []*operationfs.OperationFsStore{first, second} {
		go func() {
			<-gate
			published, err := store.RefreshPreflight(t.Context(), op.ID, maintenanceInventory())
			results <- result{published, err}
		}()
	}
	close(gate)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil {
		t.Fatalf("identical concurrent observations failed: %v, %v", a.err, b.err)
	}
	assertResultJSONEqual(t, a.op, b.op)
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, a.op)
	if saved.Preflight.Version != 1 || countResultEvents(saved, operations.PreflightUpdated) != 1 {
		t.Fatalf("identical concurrent observations duplicated a revision or event: %+v", saved)
	}
}

func TestOperationPreflightReassessesLatestMissionAndTerminalStatus(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op := createMaintenanceOperation(t, first)
	if _, err := first.RefreshPreflight(t.Context(), op.ID, maintenanceInventory()); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Mission.RequiredActions = []string{"capture"}
		next.Mission.Steps = []missions.Step{{ID: "review", RequiredActions: []string{"inspect", "capture"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	published, err := first.RefreshPreflight(t.Context(), op.ID, maintenanceInventory())
	if err != nil {
		t.Fatal(err)
	}
	want := &operations.Preflight{Version: 2, Actions: []operations.ActionReadiness{
		{ActionName: "capture"}, {ActionName: "inspect", ReadyAgentIDs: []string{"review-agent-00000001"}},
	}}
	assertResultJSONEqual(t, published.Preflight, want)
	if published.Status != operations.Waiting {
		t.Fatalf("latest mission requirement did not update status: %+v", published)
	}
	for _, status := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(status), func(t *testing.T) {
			if _, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				next.Status = status
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertTerminalRejected(t, dir, op.ID, func() (operations.Operation, error) {
				return first.RefreshPreflight(t.Context(), op.ID, maintenanceInventory())
			}, operations.ErrFinished)
		})
	}
}

func TestOperationRecoveryPreservesLatestResultsAndExpiresDueQuestions(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "active"
		if terminal {
			name = "terminal"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op := createMaintenanceOperation(t, first)
			cutoff := op.CreatedAt.Add(time.Hour)
			latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				taskID := id.TaskID()
				deadline := cutoff.Add(time.Minute)
				next.Tasks = []operations.Task{{ID: taskID, OperationID: op.ID, CommandID: taskID,
					AgentID: "review-agent-00000001", ActionName: "inspect", Status: operations.TaskSucceeded,
					DeadlineAt: &deadline, FinishedAt: &cutoff,
					Result: &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"saved":true}`)},
					Continuation: &operations.Continuation{Version: 7, DecisionBy: operations.DecisionModel,
						Status: operations.ContinuationPending, ProposedActions: []actions.NextAction{{ActionName: "capture"}}},
				}}
				next.Questions = []operations.Question{
					{ID: id.QuestionID(), Status: operations.QuestionPending, ExpiresAt: cutoff},
					{ID: id.QuestionID(), Status: operations.QuestionAnswered, ExpiresAt: cutoff, Answer: "Keep this answer", AnsweredAt: &cutoff},
					{ID: id.QuestionID(), Status: operations.QuestionPending, ExpiresAt: deadline},
				}
				next.Status = operations.Running
				if terminal {
					next.Status = operations.Cancelled
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			published, err := first.Recover(t.Context(), op.ID, cutoff)
			if err != nil {
				t.Fatal(err)
			}
			expected := latest.Clone()
			expected.Questions[0].Status = operations.QuestionExpired
			wantEvents := []operations.EventType{operations.QuestionExpiredEvent}
			if !terminal {
				expected.Status = operations.Waiting
				wantEvents = append(wantEvents, operations.OperationRecovered)
			}
			assertMaintenanceEvents(t, expected, published, started, wantEvents...)
			saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertResultJSONEqual(t, saved, published)
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := readDispatchFile(t, path)
			again, err := second.Recover(t.Context(), op.ID, cutoff)
			if err != nil {
				t.Fatal(err)
			}
			if terminal {
				assertResultJSONEqual(t, again, published)
				if !bytes.Equal(before, readDispatchFile(t, path)) {
					t.Fatal("terminal recovery without due questions changed bytes or storage revision")
				}
			} else {
				assertMaintenanceEvents(t, published, again, started, operations.OperationRecovered)
			}
		})
	}
}

func TestOperationRecoveryRetainsTerminalInFlightTasks(t *testing.T) {
	for _, status := range []operations.TaskStatus{operations.TaskRunning, operations.TaskInterrupted} {
		t.Run(string(status), func(t *testing.T) {
			dir := t.TempDir()
			store := newDispatchStore(t, dir)
			op := createMaintenanceOperation(t, store)
			cutoff := op.CreatedAt.Add(time.Hour)
			latest, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				task := operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: "inspect"})
				next.Tasks = []operations.Task{task}
				if err := next.DispatchTask(operations.TaskDispatch{TaskID: task.ID, AgentID: "review-agent-00000001", StartedAt: op.CreatedAt, DeadlineAt: cutoff}); err != nil {
					return err
				}
				next.Status, next.Tasks[0].Status = operations.Cancelled, status
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			published, err := newDispatchStore(t, dir).Recover(t.Context(), op.ID, cutoff)
			if err != nil {
				t.Fatal(err)
			}
			assertMaintenanceEvents(t, latest, published, started, operations.OperationRecovered)
		})
	}
}

func TestOperationRecoveryValidatesLatestStateBeforeExpiringQuestions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*operations.Operation)
	}{
		{"operation status", func(op *operations.Operation) { op.Status = "invalid" }},
		{"task identity", func(op *operations.Operation) { op.Tasks[0].OperationID = id.OperationID() }},
		{"duplicate task", nil},
		{"command identity", func(op *operations.Operation) { op.Tasks[0].CommandID = id.TaskID() }},
		{"execution deadline", func(op *operations.Operation) { op.Tasks[0].DeadlineAt = nil }},
		{"result action", func(op *operations.Operation) {
			op.Tasks[0].Result = &actions.ActionOutput{ActionName: "other", Output: json.RawMessage(`{}`)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op := createMaintenanceOperation(t, first)
			cutoff := op.CreatedAt.Add(time.Hour)
			if _, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				task := operations.NewTask(op.ID, "", "", actions.NextAction{ActionName: "inspect"})
				next.Tasks = []operations.Task{task}
				if err := next.DispatchTask(operations.TaskDispatch{TaskID: task.ID, AgentID: "review-agent-00000001", StartedAt: op.CreatedAt, DeadlineAt: cutoff}); err != nil {
					return err
				}
				next.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending, ExpiresAt: cutoff}}
				if tc.change != nil {
					tc.change(next)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if tc.change == nil {
				// The store rejects duplicate task IDs on write. Corrupt the
				// metadata directly to exercise recovery's persisted-state guard.
				records := openRecords(t, filepath.Join(dir, "operations", op.ID+".json"))
				if err := records.Update(t.Context(), func(next map[string]json.RawMessage) error {
					var metadata map[string]json.RawMessage
					if err := json.Unmarshal(next["operation"], &metadata); err != nil {
						return err
					}
					var taskIDs []string
					if err := json.Unmarshal(metadata["task_ids"], &taskIDs); err != nil {
						return err
					}
					metadata["task_ids"], _ = json.Marshal(append(taskIDs, taskIDs[0]))
					var err error
					next["operation"], err = json.Marshal(metadata)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			assertTerminalRejected(t, dir, op.ID, func() (operations.Operation, error) {
				return first.Recover(t.Context(), op.ID, cutoff)
			}, nil)
		})
	}
	t.Run("zero cutoff", func(t *testing.T) {
		dir := t.TempDir()
		store := newDispatchStore(t, dir)
		op := createMaintenanceOperation(t, store)
		assertTerminalRejected(t, dir, op.ID, func() (operations.Operation, error) {
			return store.Recover(t.Context(), op.ID, time.Time{})
		}, nil)
	})
}

func createMaintenanceOperation(t *testing.T, store *operationfs.OperationFsStore) operations.Operation {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	op := operations.Operation{ID: id.OperationID(), Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Mission: missions.Mission{RequiredActions: []string{"inspect"}},
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op
}

func maintenanceInventory() []agents.Agent {
	return []agents.Agent{{AgentSnapshot: agents.AgentSnapshot{ID: "review-agent-00000001", Status: agents.AgentReady,
		Actions: []actions.Action{{Name: "inspect", Status: actions.Ready}},
	}}}
}

func assertMaintenanceEvents(t *testing.T, expected, published operations.Operation, started time.Time, kinds ...operations.EventType) {
	t.Helper()
	if len(published.Events) != len(expected.Events)+len(kinds) || published.UpdatedAt.Before(started) || published.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("maintenance did not commit its events and timestamp together: %+v", published)
	}
	assertResultJSONEqual(t, published.Events[:len(expected.Events)], expected.Events)
	for i, kind := range kinds {
		event := published.Events[len(expected.Events)+i]
		if event.Type != kind || event.OperationID != expected.ID || event.Sequence != uint64(len(expected.Events)+i+1) || event.Status != expected.Status || event.CreatedAt.Before(started) || event.CreatedAt.After(published.UpdatedAt) {
			t.Fatalf("maintenance event lost its identity, status, or timestamp: %+v", event)
		}
		if kind == operations.QuestionExpiredEvent && event.QuestionID != expected.Questions[0].ID {
			t.Fatalf("expiration event does not identify its question: %+v", event)
		}
	}
	expected.UpdatedAt, expected.Events = published.UpdatedAt, published.Events
	assertResultJSONEqual(t, published, expected)
}
