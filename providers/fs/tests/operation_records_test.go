package providers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestOperationRecordsPreserveOrderAndReuseUnchangedBodies(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op := operations.Operation{ID: id.OperationID(), Status: operations.Running,
		Events: []operations.Event{{Sequence: 10, Type: operations.TaskFinished}, {Sequence: 2, Type: operations.OperationStarted}}}
	// Storage sorts record names, but public snapshots retain the caller's order.
	for _, recordID := range []string{"ffffffff-ffff-4fff-8fff-ffffffffffff", "00000000-0000-4000-8000-000000000001"} {
		op.Tasks = append(op.Tasks, operations.Task{ID: recordID, OperationID: op.ID,
			Status: operations.TaskSucceeded, Result: &actions.ActionOutput{ActionName: "inspect", Output: json.RawMessage(`{"page":1}`)}})
		question := newStoreQuestion("")
		question.ID = recordID
		op.Questions = append(op.Questions, question)
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "operations", op.ID+".json")
	before, err := openRecords(t, path).Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(before["operation"], &metadata); err != nil {
		t.Fatal(err)
	}
	if len(before) != 7 || metadata["tasks"] != nil || metadata["questions"] != nil || metadata["events"] != nil {
		t.Fatalf("operation metadata must reference separate task, question and event bodies: %s", before["operation"])
	}
	wantIDs := []string{op.Tasks[0].ID, op.Tasks[1].ID}
	assertResultJSONEqual(t, metadata["task_ids"], wantIDs)
	assertResultJSONEqual(t, metadata["question_ids"], wantIDs)
	assertResultJSONEqual(t, metadata["event_sequences"], []uint64{10, 2})
	assertResultJSONEqual(t, before["events/10"], op.Events[0])
	assertResultJSONEqual(t, before["events/2"], op.Events[1])
	files := map[string]os.FileInfo{}
	for name, body := range before {
		info, err := os.Stat(recordBlob(path, string(body)))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = info
	}
	for _, mutation := range []string{"bodies", "append event", "metadata"} {
		published, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
			switch mutation {
			case "metadata":
				next.Status = operations.Waiting
			case "append event":
				next.Events = append(next.Events, operations.Event{Sequence: 11, OperationID: op.ID, Type: operations.QuestionAsked})
			case "bodies":
				next.Tasks[0].Result.Output = json.RawMessage(`{"page":2}`)
				next.Questions[0].Text = "Updated question"
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		after, err := openRecords(t, path).Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range after {
			if name == "operation" || before[name] == nil || mutation == "bodies" && (name == "tasks/"+op.Tasks[0].ID || name == "questions/"+op.Questions[0].ID) {
				continue
			}
			info, err := os.Stat(recordBlob(path, string(body)))
			if err != nil || !bytes.Equal(before[name], body) || !os.SameFile(files[name], info) {
				t.Fatalf("unchanged body %s lost its digest or was rewritten: %v", name, err)
			}
		}
		op = published
		before = after
		for name, body := range before {
			files[name], err = os.Stat(recordBlob(path, string(body)))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	reopened := newDispatchStore(t, dir)
	saved, err := reopened.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, op)
	listed, err := reopened.List(t.Context())
	if err != nil || len(listed) != 1 {
		t.Fatalf("list failed: %+v, %v", listed, err)
	}
	assertResultJSONEqual(t, listed[0], op)
	page, err := reopened.ListTasks(t.Context(), op.ID, op.Tasks[0].ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, page, op.Tasks[1:])
	task, err := reopened.GetTask(t.Context(), op.ID, op.Tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, task, op.Tasks[0])
}

func TestOperationRecordsRejectBrokenReferences(t *testing.T) {
	for _, kind := range []string{"tasks", "questions", "events"} {
		for _, damage := range []string{"missing", "orphan", "wrong identity", "invalid body"} {
			t.Run(kind+"/"+damage, func(t *testing.T) {
				dir := t.TempDir()
				store := newDispatchStore(t, dir)
				op, _ := createContinuationOperation(t, store)
				question := newStoreQuestion(op.Tasks[0].ID)
				if _, err := store.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version); err != nil {
					t.Fatal(err)
				}
				name := "tasks/" + op.Tasks[0].ID
				if kind == "questions" {
					name = "questions/" + question.ID
				} else if kind == "events" {
					name = "events/1"
				}
				records := openRecords(t, filepath.Join(dir, "operations", op.ID+".json"))
				if err := records.Update(t.Context(), func(next map[string]json.RawMessage) error {
					switch damage {
					case "missing":
						delete(next, name)
					case "orphan":
						orphan := kind + "/" + id.TaskID()
						if kind == "events" {
							orphan = "events/99"
						}
						next[orphan] = next[name]
					case "wrong identity":
						var body map[string]json.RawMessage
						if err := json.Unmarshal(next[name], &body); err != nil {
							return err
						}
						if kind == "events" {
							body["sequence"] = json.RawMessage(`99`)
						} else {
							body["id"], _ = json.Marshal(id.TaskID())
						}
						next[name], _ = json.Marshal(body)
					case "invalid body":
						next[name] = json.RawMessage(`null`)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				reopened := newDispatchStore(t, dir)
				if _, err := reopened.Get(t.Context(), op.ID); err == nil || errors.Is(err, operations.ErrUnknownOperation) {
					t.Fatalf("broken reference was returned or hidden as absent: %v", err)
				}
				if _, err := reopened.List(t.Context()); err == nil {
					t.Fatal("list silently accepted a broken reference")
				}
				called := false
				if _, err := reopened.Update(t.Context(), op.ID, func(*operations.Operation) error { called = true; return nil }); err == nil || called {
					t.Fatalf("broken reference reached mutation: called=%v, error=%v", called, err)
				}
			})
		}
	}
}

func TestOperationRecordsQuestionTransitionsAreAtomicForReaders(t *testing.T) {
	dir := t.TempDir()
	writer, reader := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op, _ := createContinuationOperation(t, writer)
	question := newStoreQuestion(op.Tasks[0].ID)
	if _, err := writer.AskQuestion(t.Context(), op.ID, question, op.Tasks[0].Continuation.Version); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		for round := range 16 {
			answered, err := writer.AnswerQuestion(ctx, op.ID, question.ID, "yes")
			if err != nil {
				done <- err
				return
			}
			if round < 15 {
				question = newStoreQuestion(op.Tasks[0].ID)
				if _, err := writer.AskQuestion(ctx, op.ID, question, answered.Tasks[0].Continuation.Version); err != nil {
					done <- err
					return
				}
			}
		}
		done <- nil
	}()
	reads := 0
	for {
		snapshot, err := reader.Get(ctx, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		reads++
		assertAtomicQuestionRecords(t, snapshot, op)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			saved, err := newDispatchStore(t, dir).Get(ctx, op.ID)
			if err != nil || len(saved.Questions) != 16 || saved.Questions[15].Status != operations.QuestionAnswered {
				t.Fatalf("reopen lost the final answer after %d concurrent reads: %+v, %v", reads, saved, err)
			}
			assertAtomicQuestionRecords(t, saved, op)
			return
		default:
		}
	}
}

func assertAtomicQuestionRecords(t *testing.T, snapshot, original operations.Operation) {
	t.Helper()
	if len(snapshot.Tasks) != 1 || len(snapshot.Questions) == 0 || len(snapshot.Events) == 0 {
		t.Fatalf("incomplete operation snapshot: %+v", snapshot)
	}
	q := snapshot.Questions[len(snapshot.Questions)-1]
	decision, continuationStatus, operationStatus := operations.DecisionUser, operations.ContinuationWaiting, operations.Waiting
	kind, transitions := operations.QuestionAsked, 2*len(snapshot.Questions)-1
	if q.Status == operations.QuestionAnswered {
		decision, continuationStatus, operationStatus = operations.DecisionModel, operations.ContinuationPending, operations.Planning
		kind, transitions = operations.QuestionAnsweredEvent, transitions+1
		if q.Answer != "yes" || q.AnsweredAt == nil {
			t.Fatalf("answer body is incomplete: %+v", q)
		}
	} else if q.Status != operations.QuestionPending || q.Answer != "" || q.AnsweredAt != nil {
		t.Fatalf("unexpected question body: %+v", q)
	}
	assertQuestionContinuation(t, snapshot, decision, continuationStatus, q.ID, original.Tasks[0].Continuation.Version+uint64(transitions))
	event := snapshot.Events[len(snapshot.Events)-1]
	if snapshot.Status != operationStatus || len(snapshot.Events) != len(original.Events)+transitions || event.Type != kind || event.QuestionID != q.ID || event.Status != operationStatus || event.Sequence != uint64(len(snapshot.Events)) {
		t.Fatalf("question, continuation, operation status and event were torn: %+v", snapshot)
	}
}
