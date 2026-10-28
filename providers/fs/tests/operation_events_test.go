package providers_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestOperationEventRecordsMaximumSequence(t *testing.T) {
	dir := t.TempDir()
	store := fixtures.NewDispatchStore(t, dir)
	maximum := ^uint64(0)
	op := operations.Operation{ID: id.OperationID(), Events: []operations.Event{{Sequence: maximum}, {Sequence: 2}, {Sequence: 10}}}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	store = fixtures.NewDispatchStore(t, dir)
	saved, err := store.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved.Events, op.Events)
	for _, tc := range []struct {
		after uint64
		limit int
		want  []operations.Event
	}{
		{0, 2, []operations.Event{op.Events[1], op.Events[2]}},
		{3, 1, []operations.Event{op.Events[2]}},
		{10, 2, []operations.Event{op.Events[0]}},
		{maximum - 1, 1, []operations.Event{op.Events[0]}},
		{maximum, 1, []operations.Event{}},
	} {
		page, err := store.ListEvents(t.Context(), op.ID, tc.after, tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		assertResultJSONEqual(t, page, tc.want)
	}
}

func TestOperationQueriesReadOnlySelectedBodies(t *testing.T) {
	for _, kind := range []string{"tasks", "questions", "events"} {
		for _, damage := range []string{"missing", "corrupt"} {
			t.Run(kind+"/"+damage, func(t *testing.T) {
				dir := t.TempDir()
				store := fixtures.NewDispatchStore(t, dir)
				op := operations.Operation{ID: id.OperationID(),
					Tasks:     []operations.Task{{ID: id.TaskID()}, {ID: id.TaskID()}},
					Questions: []operations.Question{fixtures.Question("")},
					Events:    []operations.Event{{Sequence: 10}, {Sequence: 2}}}
				if err := store.Create(t.Context(), op); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "operations", op.ID+".json")
				records, err := fixtures.OpenRecords(t, path).Read(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				name := map[string]string{"tasks": "tasks/" + op.Tasks[1].ID,
					"questions": "questions/" + op.Questions[0].ID, "events": "events/10"}[kind]
				blob := fixtures.RecordBlob(path, string(records[name]))
				if damage == "missing" {
					err = os.Remove(blob)
				} else {
					err = os.WriteFile(blob, []byte(`{`), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
				store = fixtures.NewDispatchStore(t, dir)
				task, err := store.GetTask(t.Context(), op.ID, op.Tasks[0].ID)
				if err != nil {
					t.Fatalf("task detail read an unrelated %s body: %v", kind, err)
				}
				assertResultJSONEqual(t, task, op.Tasks[0])
				tasks, err := store.ListTasks(t.Context(), op.ID, "", 1)
				if err != nil {
					t.Fatalf("task page read an unrelated %s body: %v", kind, err)
				}
				assertResultJSONEqual(t, tasks, op.Tasks[:1])
				events, err := store.ListEvents(t.Context(), op.ID, 0, 1)
				if err != nil {
					t.Fatalf("event page read an unrelated %s body: %v", kind, err)
				}
				assertResultJSONEqual(t, events, op.Events[1:])
				if _, err := store.Get(t.Context(), op.ID); err == nil || errors.Is(err, operations.ErrUnknownOperation) {
					t.Fatalf("full snapshot must report corrupt storage: %v", err)
				}
				if kind == "tasks" {
					if _, err := store.GetTask(t.Context(), op.ID, op.Tasks[1].ID); err == nil || errors.Is(err, operations.ErrUnknownTask) {
						t.Fatalf("selected corrupt task must fail: %v", err)
					}
					if _, err := store.ListTasks(t.Context(), op.ID, op.Tasks[0].ID, 1); err == nil {
						t.Fatal("task page accepted its selected corrupt body")
					}
					if page, err := store.ListTasks(t.Context(), op.ID, op.Tasks[1].ID, 1); err != nil || page == nil || len(page) != 0 {
						t.Fatalf("empty page read the cursor's body: %+v, %v", page, err)
					}
				} else if kind == "events" {
					if _, err := store.ListEvents(t.Context(), op.ID, 2, 1); err == nil {
						t.Fatal("event page accepted its selected corrupt body")
					}
				}
			})
		}
	}
}

func TestOperationEventRecordsRejectInvalidIdentity(t *testing.T) {
	for _, kind := range []string{"zero", "duplicate", "foreign operation"} {
		t.Run(kind, func(t *testing.T) {
			store := fixtures.NewDispatchStore(t, t.TempDir())
			op := operations.Operation{ID: id.OperationID(), Events: []operations.Event{{Sequence: 1}}}
			switch kind {
			case "zero":
				op.Events[0].Sequence = 0
			case "duplicate":
				op.Events = append(op.Events, op.Events[0])
			case "foreign operation":
				op.Events[0].OperationID = id.OperationID()
			}
			if err := store.Create(t.Context(), op); err == nil {
				t.Fatal("invalid event identity was persisted")
			}
		})
	}
	for _, kind := range []string{"duplicate index", "zero index", "foreign operation", "noncanonical key"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			store := fixtures.NewDispatchStore(t, dir)
			op := operations.Operation{ID: id.OperationID(), Events: []operations.Event{{Sequence: 1}}}
			if err := store.Create(t.Context(), op); err != nil {
				t.Fatal(err)
			}
			records := fixtures.OpenRecords(t, filepath.Join(dir, "operations", op.ID+".json"))
			if err := records.Update(t.Context(), func(next map[string]json.RawMessage) error {
				switch kind {
				case "duplicate index", "zero index":
					var metadata map[string]json.RawMessage
					if err := json.Unmarshal(next["operation"], &metadata); err != nil {
						return err
					}
					metadata["event_sequences"] = json.RawMessage(`[1,1]`)
					if kind == "zero index" {
						metadata["event_sequences"] = json.RawMessage(`[0]`)
					}
					next["operation"], _ = json.Marshal(metadata)
				case "foreign operation":
					event := op.Events[0]
					event.OperationID = id.OperationID()
					next["events/1"], _ = json.Marshal(event)
				case "noncanonical key":
					next["events/01"] = next["events/1"]
					delete(next, "events/1")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(t.Context(), op.ID); err == nil || errors.Is(err, operations.ErrUnknownOperation) {
				t.Fatalf("invalid event index or body returned in snapshot: %v", err)
			}
			if _, err := store.ListEvents(t.Context(), op.ID, 0, 1); err == nil || errors.Is(err, operations.ErrUnknownOperation) {
				t.Fatalf("invalid event index or body returned in page: %v", err)
			}
		})
	}
}
