package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	executionfs "github.com/defos-ai/commander/providers/fs/executions"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestOperationUpdatesAreAtomicAcrossStoreInstances(t *testing.T) {
	dir := t.TempDir()
	first, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	op := operations.Operation{ID: id.OperationID(), Status: operations.Planning}
	if err := first.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	gate, results := make(chan struct{}), make(chan error, 2)

	for _, store := range []*operationfs.OperationFsStore{first, second} {
		go func() {
			<-gate
			_, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				next.Events = append(next.Events, operations.Event{Sequence: uint64(len(next.Events) + 1)})
				return nil
			})
			results <- err
		}()
	}
	close(gate)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reopened.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Events) != 2 || stored.Events[0].Sequence != 1 || stored.Events[1].Sequence != 2 {
		t.Fatalf("concurrent mutations lost an event: %+v", stored)
	}

	rejected := errors.New("invalid transition")
	_, err = reopened.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Status = operations.Completed
		next.Events[0].Sequence = 100
		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("expected callback rejection, got %v", err)
	}
	after, err := first.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(after, stored) {
		t.Fatalf("rejected mutation changed storage: %+v, %v", after, err)
	}
	payload, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if err := json.Unmarshal(payload, &public); err != nil {
		t.Fatal(err)
	}
	if _, exists := public["version"]; exists {
		t.Fatal("storage revision escaped into the domain operation")
	}
}

func TestOperationWritesDoNotBlockOtherOperations(t *testing.T) {
	dir := t.TempDir()
	first, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := operations.Operation{ID: id.OperationID(), Status: operations.Planning}
	b := operations.Operation{ID: id.OperationID(), Status: operations.Planning}
	for _, op := range []operations.Operation{a, b} {
		if err := first.Create(t.Context(), op); err != nil {
			t.Fatal(err)
		}
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer func() {
		close(release)
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("first operation update failed after release: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("first operation update did not finish after release")
		}
	}()
	go func() {
		_, err := first.Update(t.Context(), a.ID, func(next *operations.Operation) error {
			next.Status = operations.Running
			close(entered)
			<-release
			return nil
		})
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first operation update did not acquire its lock")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	previous, err := second.Get(ctx, a.ID)
	if err != nil || previous.Status != operations.Planning {
		t.Fatalf("reading during an update must return the previous snapshot without waiting: %+v, %v", previous, err)
	}
	if _, err := second.Get(ctx, b.ID); err != nil {
		t.Fatalf("reading another operation waited for the first writer: %v", err)
	}
	updated, err := second.Update(ctx, b.ID, func(next *operations.Operation) error {
		next.Status = operations.Completed
		return nil
	})
	if err != nil || updated.Status != operations.Completed {
		t.Fatalf("updating another operation waited for the first writer: %+v, %v", updated, err)
	}
	stored, err := second.Get(ctx, b.ID)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatalf("independent update was not saved: %+v, %v", stored, err)
	}
}

func TestOperationRoundTripAndListingIgnoreUnrelatedFiles(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := operations.Operation{ID: "00000000-0000-4000-8000-000000000001", Status: operations.Planning,
		Input: json.RawMessage(`{}`),
		Mission: missions.Mission{Name: "inspect", OptionalActions: []string{"publish"}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
			RequiredActions:   []string{"inspect"},
			ActionDefinitions: fixtures.Mission().ActionDefinitions,
			Rules: missions.RuleSet{
				missions.AggregateResults:  {ForActions: []string{"discover"}},
				missions.DecideNextActions: {ForActions: []string{"inspect"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
			}},
		Tasks:     []operations.Task{{ID: id.New(), Input: json.RawMessage(`1`)}},
		Events:    []operations.Event{{Sequence: 1, Type: operations.QuestionAsked}},
		Questions: []operations.Question{{ID: id.New(), Text: "Proceed?", Options: []string{"yes", "no"}}}}
	second := operations.Operation{ID: "00000000-0000-4000-8000-000000000002", Status: operations.Completed,
		Input: json.RawMessage(`{}`), Mission: first.Mission.Clone()}
	for _, op := range []operations.Operation{second, first} {
		if err := store.Create(t.Context(), op); err != nil {
			t.Fatal(err)
		}
		stored, err := store.Get(t.Context(), op.ID)
		if err != nil || !reflect.DeepEqual(stored, op.Clone()) {
			t.Fatalf("operation round trip changed the saved snapshot: %+v, %v", stored, err)
		}
		stored.Mission.OptionalActions[0] = "changed"
		stored.Mission.ActionDefinitions[0].Name = "changed"
		stored.Mission.ActionDefinitions[0].RequiredSecrets[0] = "CHANGED"
		stored.Mission.ActionDefinitions[0].InputSchema[0] = '['
		stored.Mission.ActionDefinitions[0].OutputSchema[0] = '['
		stored.Mission.Rules[missions.AggregateResults].ForActions[0] = "changed"
		stored.Mission.Rules[missions.DecideNextActions].Params.DecisionBy = "changed"
	}
	if err := store.Create(t.Context(), first); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("duplicate operation creation must conflict: %v", err)
	}
	unknown := id.OperationID()
	for _, name := range []string{unknown + ".json.lock", "." + unknown + ".json-pending.tmp", "notes.json"} {
		if err := os.WriteFile(filepath.Join(dir, "operations", name), []byte("not a record"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []operations.Operation{first.Clone(), second.Clone()}) {
		t.Fatalf("list must return sorted operation snapshots only: %+v, %v", items, err)
	}
	if _, err := store.Get(t.Context(), unknown); !errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("a lock file must not invent an operation: %v", err)
	}
	called := false
	if _, err := store.Update(t.Context(), unknown, func(*operations.Operation) error { called = true; return nil }); !errors.Is(err, operations.ErrUnknownOperation) || called {
		t.Fatalf("updating a missing operation must fail without calling the mutation: called=%v, err=%v", called, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "operations", unknown+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading or updating an unknown ID created an operation file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "operations.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("operation store created the shared collection: %v", err)
	}
}

func TestOperationFileMustMatchItsID(t *testing.T) {
	dir := t.TempDir()
	store, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	op := operations.Operation{ID: id.OperationID(), Status: operations.Planning}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	otherID := id.OperationID()
	if err := os.Rename(filepath.Join(dir, "operations", op.ID+".json"), filepath.Join(dir, "operations", otherID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "operations", op.ID+".json.records"), filepath.Join(dir, "operations", otherID+".json.records")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(t.Context(), otherID); err == nil || errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("mismatched file identity must report corrupt storage: %v", err)
	}
	called := false
	if _, err := store.Update(t.Context(), otherID, func(*operations.Operation) error { called = true; return nil }); err == nil || called {
		t.Fatalf("mismatched file identity reached mutation: called=%v, err=%v", called, err)
	}
	if _, err := store.List(t.Context()); err == nil {
		t.Fatal("list silently accepted a mismatched operation identity")
	}
}

func TestOperationEventsPagination(t *testing.T) {
	store, err := operationfs.NewOperationFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	op := operations.Operation{ID: id.OperationID(), Events: []operations.Event{
		{Sequence: 10, Type: operations.OperationCompleted},
		{Sequence: 2, Type: operations.OperationStarted},
		{Sequence: 7, Type: operations.TaskFinished},
	}}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		after uint64
		limit int
		want  []operations.Event
	}{
		{"first page", 0, 2, []operations.Event{op.Events[1], op.Events[2]}},
		{"second page", 7, 2, []operations.Event{op.Events[0]}},
		{"cursor between events", 3, 1, []operations.Event{op.Events[2]}},
		{"at end", 10, 2, []operations.Event{}},
		{"beyond end", 100, 2, []operations.Event{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.ListEvents(t.Context(), op.ID, tc.after, tc.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) != len(tc.want) || len(page) > 0 && !reflect.DeepEqual(page, tc.want) {
				t.Fatalf("events after %d: got %+v, want %+v", tc.after, page, tc.want)
			}
			if len(page) > 0 {
				page[0].Sequence, page[0].Type = 99, operations.OperationFailed
			}
		})
	}
	stored, err := store.Get(t.Context(), op.ID)
	if err != nil || !reflect.DeepEqual(stored.Events, op.Events) {
		t.Fatalf("querying or modifying a page changed stored events: %+v, %v", stored.Events, err)
	}
	for _, limit := range []int{0, -1} {
		if _, err := store.ListEvents(t.Context(), op.ID, 0, limit); err == nil {
			t.Errorf("invalid event limit %d was accepted", limit)
		}
	}
	if _, err := store.ListEvents(t.Context(), id.OperationID(), 0, 2); !errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("missing operation must not look like an empty event page: %v", err)
	}
}

func TestOperationTaskQueries(t *testing.T) {
	store, err := operationfs.NewOperationFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	first := operations.Operation{ID: id.OperationID(), Tasks: []operations.Task{
		{ID: "00000000-0000-4000-8000-000000000002", ActionName: "inspect", Status: operations.TaskSucceeded,
			Input: json.RawMessage(`1`), FinishedAt: &finished,
			Result: &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`2`),
				NextActions: []actions.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`3`)}}},
			Continuation: &operations.Continuation{Version: 2, Status: operations.ContinuationPending, DecisionBy: operations.DecisionModel,
				ProposedActions: []actions.NextAction{{ActionName: "follow-up", Input: json.RawMessage(`4`)}}, CreatedTaskIDs: []string{id.New()}}},
		{ID: "00000000-0000-4000-8000-000000000001", ActionName: "follow-up", Status: operations.TaskQueued},
	}}
	other := operations.Operation{ID: id.OperationID(), Tasks: []operations.Task{{ID: id.New(), ActionName: "unrelated", Status: operations.TaskQueued}}}
	empty := operations.Operation{ID: id.OperationID()}
	for _, op := range []*operations.Operation{&first, &other, &empty} {
		for i := range op.Tasks {
			op.Tasks[i].OperationID = op.ID
		}
		if err := store.Create(t.Context(), *op); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := store.ListTasks(t.Context(), first.ID, "", 100)
	if err != nil || !reflect.DeepEqual(listed, first.Clone().Tasks) {
		t.Fatalf("task list must preserve assignment order and complete details: %+v, %v", listed, err)
	}
	detail, err := store.GetTask(t.Context(), first.ID, first.Tasks[0].ID)
	if err != nil || !reflect.DeepEqual(detail, listed[0]) {
		t.Fatalf("task detail differs from its operation snapshot: %+v, %v", detail, err)
	}
	for _, task := range []*operations.Task{&listed[0], &detail} {
		task.Input[0] = '9'
		task.Result.Output[0] = '9'
		task.Result.NextActions[0].Input[0] = '9'
		task.Continuation.ProposedActions[0].Input[0] = '9'
		task.Continuation.CreatedTaskIDs[0] = "changed"
		*task.FinishedAt = task.FinishedAt.Add(time.Hour)
	}
	for _, op := range []operations.Operation{first, other} {
		stored, err := store.ListTasks(t.Context(), op.ID, "", 100)
		if err != nil || !reflect.DeepEqual(stored, op.Clone().Tasks) {
			t.Fatalf("modifying query results changed stored tasks or another operation: %+v, %v", stored, err)
		}
	}
	for _, tc := range []struct {
		name  string
		after string
		want  []operations.Task
	}{
		{"first page", "", first.Tasks[:1]},
		{"second page", first.Tasks[0].ID, first.Tasks[1:]},
		{"at end", first.Tasks[1].ID, []operations.Task{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.ListTasks(t.Context(), first.ID, tc.after, 1)
			if err != nil || !reflect.DeepEqual(page, tc.want) {
				t.Fatalf("task page after %q: got %+v, want %+v; %v", tc.after, page, tc.want, err)
			}
		})
	}
	last, err := store.GetTask(t.Context(), first.ID, first.Tasks[1].ID)
	if err != nil || !reflect.DeepEqual(last, first.Tasks[1]) {
		t.Fatalf("get task must find a task beyond the first page: %+v, %v", last, err)
	}
	updated, err := store.Update(t.Context(), first.ID, func(op *operations.Operation) error {
		op.Tasks[0].Status = operations.TaskRunning
		op.Tasks[1].Status = operations.TaskSucceeded
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.ListTasks(t.Context(), first.ID, first.Tasks[0].ID, 1)
	if err != nil || !reflect.DeepEqual(page, updated.Tasks[1:]) {
		t.Fatalf("status changes reordered tasks or invalidated their cursor: %+v, %v", page, err)
	}
	noTasks, err := store.ListTasks(t.Context(), empty.ID, "", 100)
	if err != nil || noTasks == nil || len(noTasks) != 0 {
		t.Fatalf("a known operation without tasks must return an empty list: %+v, %v", noTasks, err)
	}
	for _, taskID := range []string{id.New(), other.Tasks[0].ID} {
		if _, err := store.GetTask(t.Context(), first.ID, taskID); !errors.Is(err, operations.ErrUnknownTask) {
			t.Fatalf("missing or foreign task %s must not be returned: %v", taskID, err)
		}
		if _, err := store.ListTasks(t.Context(), first.ID, taskID, 1); !errors.Is(err, operations.ErrUnknownTask) {
			t.Fatalf("missing or foreign task cursor %s must fail: %v", taskID, err)
		}
	}
	unknown := id.OperationID()
	if _, err := store.ListTasks(t.Context(), unknown, "", 100); !errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("unknown operation must not return an empty task list: %v", err)
	}
	if _, err := store.GetTask(t.Context(), unknown, first.Tasks[0].ID); !errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("task query lost unknown operation error: %v", err)
	}
	for _, taskID := range []string{"", "../task", "00000000000040008000000000000001"} {
		if _, err := store.GetTask(t.Context(), first.ID, taskID); err == nil || errors.Is(err, operations.ErrUnknownTask) {
			t.Errorf("malformed task ID %q must fail validation: %v", taskID, err)
		}
		if taskID != "" {
			if _, err := store.ListTasks(t.Context(), first.ID, taskID, 1); err == nil || errors.Is(err, operations.ErrUnknownTask) {
				t.Errorf("malformed task cursor %q must fail validation: %v", taskID, err)
			}
		}
	}
	for _, limit := range []int{0, -1} {
		if _, err := store.ListTasks(t.Context(), first.ID, "", limit); err == nil {
			t.Errorf("invalid task limit %d was accepted", limit)
		}
	}
}

func TestOperationWritesDistinguishUnpublishedAndUncertainSnapshots(t *testing.T) {
	for _, name := range []string{"update", "create", "unchanged update"} {
		t.Run(name, func(t *testing.T) {
			create, unchanged := name == "create", name == "unchanged update"
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			store, err := operationfs.NewOperationFsStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			other := operations.Operation{ID: id.OperationID(), Status: operations.Completed}
			if err := store.Create(t.Context(), other); err != nil {
				t.Fatal(err)
			}
			other, err = store.Get(t.Context(), other.ID)
			if err != nil {
				t.Fatal(err)
			}
			op := other.Clone()
			op.ID, op.Status = id.OperationID(), operations.Planning
			if !create {
				if err := store.Create(t.Context(), op); err != nil {
					t.Fatal(err)
				}
			}
			operationDir := filepath.Join(dir, "operations")
			var restore func()
			calls := 0
			var published operations.Operation

			if create {
				restore = fixtures.DenyDirectoryRead(t, operationDir)
				err = store.Create(t.Context(), op)
			} else {
				published, err = store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
					calls++
					restore = fixtures.DenyDirectoryRead(t, operationDir)
					if !unchanged {
						next.Status = operations.Running
						next.Events = append(next.Events, operations.Event{Sequence: 1})
					}
					return nil
				})
			}
			if restore != nil {
				restore()
			}

			if !errors.Is(err, fs.ErrPermission) || errors.Is(err, operations.ErrWriteUncertain) != unchanged {
				t.Fatalf("directory sync failure lost its cause or publication state: %v", err)
			}
			if !create && calls != 1 {
				t.Fatalf("write failure replayed its mutation: calls=%d", calls)
			}
			if unchanged {
				if !reflect.DeepEqual(published, op) {
					t.Fatalf("uncertain durability confirmation must return the visible snapshot: %+v", published)
				}
			} else if published.ID != "" {
				t.Fatalf("failed write returned an unpublished snapshot: %+v", published)
			}
			if create {
				if _, err := store.Get(t.Context(), op.ID); !errors.Is(err, operations.ErrUnknownOperation) {
					t.Fatalf("failed creation published an operation: %v", err)
				}
				if err := store.Create(t.Context(), op); err != nil {
					t.Fatalf("failed creation could not be retried: %v", err)
				}
			}
			reopened, err := operationfs.NewOperationFsStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []operations.Operation{op, other} {
				data, err := os.ReadFile(filepath.Join(operationDir, expected.ID+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var manifest []struct {
					Version uint64 `json:"version"`
				}
				if err := json.Unmarshal(data, &manifest); err != nil {
					t.Fatal(err)
				}
				if len(manifest) != 1 || manifest[0].Version != 1 {
					t.Fatalf("failed or unchanged write advanced the storage revision: %+v", manifest)
				}
				saved, err := reopened.Get(t.Context(), expected.ID)
				if err != nil || !reflect.DeepEqual(saved, expected) {
					t.Fatalf("write failure changed the persisted snapshot: %+v, %v", saved, err)
				}
			}
			confirmed, err := reopened.Update(t.Context(), op.ID, func(*operations.Operation) error { return nil })
			if err != nil || !reflect.DeepEqual(confirmed, op) {
				t.Fatalf("could not confirm durability after permissions recovered: %+v, %v", confirmed, err)
			}
		})
	}
}

func TestExecutionClaimAndOutcomeRemainStableAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := executionfs.NewExecutionFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := executionfs.NewExecutionFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	call := actions.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)}
	command, err := agentcommands.NewRunAction("review-agent-00000001", call)
	if err != nil {
		t.Fatal(err)
	}
	type claim struct {
		created bool
		err     error
	}
	gate, claims := make(chan struct{}), make(chan claim, 2)

	for _, store := range []*executionfs.ExecutionFsStore{first, second} {
		go func() { <-gate; _, created, err := store.Claim(t.Context(), command); claims <- claim{created, err} }()
	}
	close(gate)
	a, b := <-claims, <-claims
	if a.err != nil || b.err != nil || a.created == b.created {
		t.Fatalf("claim must have one owner: %+v %+v", a, b)
	}
	result, err := messages.NewMessage(messages.ActionSucceeded, command.AgentID, actions.ActionOutput{ActionName: call.ActionName, Output: json.RawMessage(`{"ok":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	result.CorrelationID, result.Work = command.ID, command.Work
	if err := first.Complete(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	if err := second.Complete(t.Context(), result); err != nil {
		t.Fatalf("identical result replay failed: %v", err)
	}
	reopened, err := executionfs.NewExecutionFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, created, err := reopened.Claim(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}

	if created || saved.Result == nil || saved.CompletedAt == nil || !reflect.DeepEqual(*saved.Result, result) {
		t.Fatalf("reopen lost the accepted outcome: %+v", saved)
	}
	conflicting := command
	conflicting.Payload = json.RawMessage(`{"action_name":"inspect","input":{"page":2}}`)
	if _, _, err := reopened.Claim(t.Context(), conflicting); !errors.Is(err, executions.ErrExecutionConflict) {
		t.Fatalf("command identity was reused: %v", err)
	}
	conflicting = result
	conflicting.Payload = json.RawMessage(`{"action_name":"inspect","output":{"ok":false}}`)
	if err := reopened.Complete(t.Context(), conflicting); !errors.Is(err, executions.ErrExecutionConflict) {
		t.Fatalf("accepted result was replaced: %v", err)
	}
	unchanged, err := reopened.Get(t.Context(), command.AgentID, command.ID)
	if err != nil || !reflect.DeepEqual(saved, unchanged) {
		t.Fatalf("conflict changed saved execution: %+v, %v", unchanged, err)
	}
}

func TestExecutionClaimPreservesOriginalDeadlineAcrossReopen(t *testing.T) {
	deadline := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	for _, tc := range []struct {
		name      string
		expiresAt time.Time
	}{
		{"no deadline", time.Time{}},
		{"nanosecond deadline", deadline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := executionfs.NewExecutionFsStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			command, err := agentcommands.NewRunAction("review-agent-00000001", actions.ActionCall{ActionName: "inspect", Input: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			command.ExpiresAt = tc.expiresAt
			saved, claimed, err := store.Claim(t.Context(), command)
			if err != nil || !claimed || !reflect.DeepEqual(saved.Command, command) {
				t.Fatalf("initial claim changed command: %+v, claimed=%t, error=%v", saved, claimed, err)
			}
			reopened, err := executionfs.NewExecutionFsStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, suggested := range []time.Time{deadline.Add(time.Hour), time.Time{}, deadline.Add(-time.Hour)} {
				retry := command
				retry.CreatedAt = retry.CreatedAt.Add(time.Hour)
				retry.ExpiresAt = suggested
				restored, claimed, err := reopened.Claim(t.Context(), retry)
				if err != nil || claimed || !reflect.DeepEqual(restored, saved) {
					t.Fatalf("retry with deadline %v changed reservation: %+v, claimed=%t, error=%v", suggested, restored, claimed, err)
				}
			}
		})
	}
}
