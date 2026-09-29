package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	agentcommands "commander/framework/comms/clients/agent/commands"
	"commander/framework/comms/messages"
	"commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
	executionfs "commander/providers/fs/controller/executions"
	operationfs "commander/providers/fs/controller/operations"
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

func TestOperationFilesKeepCompleteSnapshotsAndListOnlyRecords(t *testing.T) {
	dir := t.TempDir()
	store, err := operationfs.NewOperationFsStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.Root() != dir {
		t.Fatalf("operation root must remain the database root: %q", store.Root())
	}
	first := operations.Operation{ID: "00000000-0000-4000-8000-000000000001", Status: operations.Planning,
		Input:     json.RawMessage(`{}`),
		Mission:   missions.Mission{Name: "inspect", InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
		Tasks:     []operations.Task{{ID: id.New(), Input: json.RawMessage(`1`)}},
		Events:    []operations.Event{{Sequence: 1, Type: operations.QuestionAsked}},
		Questions: []operations.Question{{ID: id.New(), Text: "Proceed?", Options: []string{"yes", "no"}}}}
	second := operations.Operation{ID: "00000000-0000-4000-8000-000000000002", Status: operations.Completed,
		Input: json.RawMessage(`{}`), Mission: first.Mission.Clone()}
	for _, op := range []operations.Operation{second, first} {
		if err := store.Create(t.Context(), op); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, "operations", op.ID+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var records []struct {
			operations.Operation
			Version uint64 `json:"version"`
		}
		if err := json.Unmarshal(data, &records); err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || records[0].Version != 1 || !reflect.DeepEqual(records[0].Operation, op.Clone()) {
			t.Fatalf("operation file must retain its complete snapshot: %+v", records)
		}
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

func TestOperationWritesReportUncertainDurability(t *testing.T) {
	for _, name := range []string{"update", "create"} {
		t.Run(name, func(t *testing.T) {
			create := name == "create"
			dir := t.TempDir()
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
			expected, version := op.Clone(), uint64(1)
			if !create {
				if err := store.Create(t.Context(), op); err != nil {
					t.Fatal(err)
				}
				expected.Status, version = operations.Running, 2
				expected.Events = []operations.Event{{Sequence: 1}}
			}
			operationDir := filepath.Join(dir, "operations")
			restore := denyDirectoryRead(t, operationDir)
			calls := 0
			var published operations.Operation

			if create {
				err = store.Create(t.Context(), op)
			} else {
				published, err = store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
					calls++
					next.Status = operations.Running
					next.Events = append(next.Events, operations.Event{Sequence: 1})
					return nil
				})
			}
			restore()

			if !errors.Is(err, operations.ErrWriteUncertain) || !errors.Is(err, fs.ErrPermission) {
				t.Fatalf("post-rename sync failure lost its uncertainty or cause: %v", err)
			}
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr.Op != "open" || pathErr.Path != filepath.Join(store.Root(), "operations") {
				t.Fatalf("expected failure opening the directory for sync, got %v", err)
			}
			if !create && (calls != 1 || !reflect.DeepEqual(published, expected)) {
				t.Fatalf("uncertain update must return its published snapshot without replay: calls=%d, snapshot=%+v", calls, published)
			}
			for _, operationID := range []string{op.ID, other.ID} {
				data, err := os.ReadFile(filepath.Join(operationDir, operationID+".json"))
				if err != nil {
					t.Fatal(err)
				}
				var records []struct {
					operations.Operation
					Version uint64 `json:"version"`
				}
				if err := json.Unmarshal(data, &records); err != nil {
					t.Fatal(err)
				}
				if len(records) != 1 {
					t.Fatalf("operation file must contain one snapshot: %+v", records)
				}
				record := records[0]
				want, wantVersion := expected, version
				if operationID == other.ID {
					want, wantVersion = other, 1
				}
				if record.Version != wantVersion || !reflect.DeepEqual(record.Operation, want) {
					t.Fatalf("uncertain write changed the wrong snapshot or revision: %+v", record)
				}
			}
			reopened, err := operationfs.NewOperationFsStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := reopened.Get(t.Context(), op.ID)
			if err != nil || !reflect.DeepEqual(saved, expected) {
				t.Fatalf("reopening lost the published snapshot: %+v, %v", saved, err)
			}
			confirmed, err := reopened.Update(t.Context(), op.ID, func(*operations.Operation) error { return nil })
			if err != nil || !reflect.DeepEqual(confirmed, expected) {
				t.Fatalf("could not confirm durability after permissions recovered: %+v, %v", confirmed, err)
			}
		})
	}
}

func denyDirectoryRead(t *testing.T, dir string) func() {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions and an unprivileged user")
	}
	restore := func() {
		t.Helper()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	if err := os.Chmod(dir, 0300); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(dir)
	if err == nil {
		file.Close()
		t.Skip("directory read permissions are not enforced")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("directory permission probe failed unexpectedly: %v", err)
	}
	return restore
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
