package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"commander/framework/domain/operations"
	"commander/framework/id"
	operationfs "commander/providers/fs/controller/operations"
)

func TestOperationCompletionPublishesReportAndDecisionsTogether(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op := createCompletionOperation(t, first)
	latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		// A decision already resolved by another writer must retain its version.
		task := next.Clone().Tasks[0]
		task.ID = id.TaskID()
		task.CommandID = task.ID
		task.Continuation.Status = operations.ContinuationResolved
		next.Tasks = append(next.Tasks, task)
		next.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionAnswered, Answer: "Proceed"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reportID := id.ReportID()
	output := json.RawMessage(`{"summary":"done"}`)
	started := time.Now().UTC()

	published, err := first.Complete(t.Context(), op.ID, reportID, output)

	if err != nil {
		t.Fatal(err)
	}
	expected := latest.Clone()
	expected.Status, expected.ReportID, expected.Output = operations.Completed, reportID, bytes.Clone(output)
	expected.Tasks[0].Continuation.Status = operations.ContinuationResolved
	expected.Tasks[0].Continuation.Version++
	assertTerminalSnapshot(t, expected, published, operations.OperationCompleted, started)
	// Neither caller-owned output nor the returned snapshot may alias storage.
	output[0] = '['
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, published, saved)
	published.Output[0] = '['
	published.Tasks[0].Continuation.Version++
	again, err := first.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, again, saved)
}

func TestOperationCompletionRejectsChangedStoredEligibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*operations.Operation)
	}{
		{"queued task", func(op *operations.Operation) { op.Tasks[0].Status = operations.TaskQueued }},
		{"running task", func(op *operations.Operation) { op.Tasks[0].Status = operations.TaskRunning }},
		{"pending question", func(op *operations.Operation) {
			op.Questions = append(op.Questions, operations.Question{ID: id.QuestionID(), Status: operations.QuestionPending})
		}},
		{"automatic continuation", func(op *operations.Operation) {
			op.Tasks[0].Continuation.DecisionBy = operations.DecisionCode
			op.Tasks[0].Continuation.Status = operations.ContinuationWaiting
		}},
		{"operator continuation", func(op *operations.Operation) {
			op.Tasks[0].Continuation.DecisionBy = operations.DecisionUser
		}},
		{"output schema", func(op *operations.Operation) {
			op.Mission.OutputSchema = json.RawMessage(`{"type":"object","required":["evidence"]}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op := createCompletionOperation(t, first)
			if _, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				tc.change(next)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := readDispatchFile(t, path)

			published, err := first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"summary":"done"}`))

			if err == nil || published.ID != "" {
				t.Fatalf("completion accepted stale eligibility: %+v, %v", published, err)
			}
			if tc.name == "pending question" && !errors.Is(err, operations.ErrQuestionPending) {
				t.Fatalf("pending question lost its error identity: %v", err)
			}
			if !bytes.Equal(before, readDispatchFile(t, path)) {
				t.Fatal("rejected completion changed the latest snapshot or storage revision")
			}
		})
	}
}

func TestOperationTerminalWritesRejectInvalidInputsAndTerminalState(t *testing.T) {
	dir := t.TempDir()
	store := newDispatchStore(t, dir)
	op := createCompletionOperation(t, store)
	complete := func(reportID string, output json.RawMessage) func() (operations.Operation, error) {
		return func() (operations.Operation, error) { return store.Complete(t.Context(), op.ID, reportID, output) }
	}
	fail := func(reason string) func() (operations.Operation, error) {
		return func() (operations.Operation, error) { return store.Fail(t.Context(), op.ID, reason) }
	}
	validReportID, validOutput := id.ReportID(), json.RawMessage(`{"summary":"done"}`)
	for _, tc := range []struct {
		name string
		call func() (operations.Operation, error)
	}{
		{"empty report ID", complete("", validOutput)},
		{"noncanonical report ID", complete(strings.ToUpper("123e4567-e89b-12d3-a456-426614174000"), validOutput)},
		{"malformed output", complete(validReportID, json.RawMessage(`{"summary":`))},
		{"invalid output", complete(validReportID, json.RawMessage(`{}`))},
		{"empty failure", fail("")},
		{"blank failure", fail(" \t\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTerminalRejected(t, dir, op.ID, tc.call, nil)
		})
	}
	for _, status := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(status), func(t *testing.T) {
			if _, err := newDispatchStore(t, dir).Update(t.Context(), op.ID, func(next *operations.Operation) error {
				next.Status = status
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertTerminalRejected(t, dir, op.ID, complete(validReportID, validOutput), operations.ErrFinished)
			assertTerminalRejected(t, dir, op.ID, fail("Cannot continue"), operations.ErrFinished)
		})
	}
}

func TestOperationCompetingTerminalWritesPublishOneWinner(t *testing.T) {
	for _, competingStatus := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(competingStatus), func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op := createCompletionOperation(t, first)
			compete := func() (operations.Operation, error) {
				switch competingStatus {
				case operations.Completed:
					return second.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"summary":"second"}`))
				case operations.Failed:
					return second.Fail(t.Context(), op.ID, "Cannot continue")
				default:
					return second.Cancel(t.Context(), op.ID)
				}
			}
			type result struct {
				op  operations.Operation
				err error
			}
			gate, results := make(chan struct{}), make(chan result, 2)
			for _, call := range []func() (operations.Operation, error){
				func() (operations.Operation, error) {
					return first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"summary":"first"}`))
				}, compete,
			} {
				go func() {
					<-gate
					published, err := call()
					results <- result{published, err}
				}()
			}
			close(gate)
			winner, loser := <-results, <-results
			if winner.err != nil {
				winner, loser = loser, winner
			}
			if winner.err != nil || !errors.Is(loser.err, operations.ErrFinished) || loser.op.ID != "" {
				t.Fatalf("terminal writes must have one winner: %v, %v", winner.err, loser.err)
			}
			saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertResultJSONEqual(t, saved, winner.op)
			if len(saved.Events) != len(op.Events)+1 || countResultEvents(saved, operations.OperationCompleted)+countResultEvents(saved, operations.OperationFailed)+countResultEvents(saved, operations.OperationCancelled) != 1 {
				t.Fatalf("terminal writers lost or duplicated the winning event: %+v", saved.Events)
			}
		})
	}
}

func TestOperationFailureCancelsLatestQueuedWorkAndPreservesEvidence(t *testing.T) {
	dir := t.TempDir()
	first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
	op, result := createResultOperation(t, first)
	latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		for _, status := range []operations.TaskStatus{operations.TaskQueued, operations.TaskInterrupted, operations.TaskSucceeded, operations.TaskQueued} {
			task := next.Tasks[0]
			task.ID, task.Status = id.TaskID(), status
			task.CommandID = task.ID
			if status == operations.TaskSucceeded {
				task.Result = result.Result
			}
			next.Tasks = append(next.Tasks, task)
		}
		next.Questions = []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending, Text: "Keep this question"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	published, err := first.Fail(t.Context(), op.ID, "Cannot continue")
	if err != nil {
		t.Fatal(err)
	}
	expected := latest.Clone()
	expected.Status, expected.Error = operations.Failed, "Cannot continue"
	for i := range expected.Tasks {
		if expected.Tasks[i].Status == operations.TaskQueued {
			finished := published.Tasks[i].FinishedAt
			if finished == nil || finished.Before(started) || finished.After(published.UpdatedAt) {
				t.Fatalf("queued task lost its cancellation timestamp: %+v", published.Tasks[i])
			}
			expected.Tasks[i].Status, expected.Tasks[i].FinishedAt = operations.TaskCancelled, finished
		}
	}
	assertTerminalSnapshot(t, expected, published, operations.OperationFailed, started)
	if published.Events[len(published.Events)-1].Message != expected.Error {
		t.Fatal("failure event lost its reason")
	}
	saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, published)
}

func TestOperationFailureSerializesWithAutomaticFollowupsAndLateResults(t *testing.T) {
	for _, order := range []string{"failure first", "result first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			dir := t.TempDir()
			first, second := newDispatchStore(t, dir), newDispatchStore(t, dir)
			op, request := createResultOperation(t, first)
			fail := func() error { _, err := first.Fail(t.Context(), op.ID, "Cannot continue"); return err }
			finish := func() error { _, err := second.FinishTask(t.Context(), op.ID, request); return err }
			var a, b error
			switch order {
			case "failure first":
				a, b = fail(), finish()
			case "result first":
				a, b = finish(), fail()
			default:
				gate, results := make(chan struct{}), make(chan error, 2)
				for _, call := range []func() error{fail, finish} {
					go func() { <-gate; results <- call() }()
				}
				close(gate)
				a, b = <-results, <-results
			}
			if a != nil || b != nil {
				t.Fatalf("failure or task result was lost: %v, %v", a, b)
			}
			saved, err := newDispatchStore(t, dir).Get(t.Context(), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Status != operations.Failed || saved.Error != "Cannot continue" || saved.Tasks[0].Status != operations.TaskSucceeded || countResultEvents(saved, operations.OperationFailed) != 1 || countResultEvents(saved, operations.TaskFinished) != 1 {
				t.Fatalf("failure or result was lost or duplicated: %+v", saved)
			}
			assertResultJSONEqual(t, saved.Tasks[0].Result, request.Result)
			if order == "failure first" && len(saved.Tasks) != 1 || order == "result first" && len(saved.Tasks) != 3 || len(saved.Tasks) != 1 && len(saved.Tasks) != 3 {
				t.Fatalf("automatic follow-ups escaped their result transaction: %+v", saved.Tasks)
			}
			for _, child := range saved.Tasks[1:] {
				if child.Status != operations.TaskCancelled || child.FinishedAt == nil {
					t.Fatalf("failure left an automatic child runnable: %+v", child)
				}
			}
			if countResultEvents(saved, operations.TaskAssigned) != len(saved.Tasks)-1 {
				t.Fatal("automatic child assignments lost their events")
			}
		})
	}
}

func createCompletionOperation(t *testing.T, store *operationfs.OperationFsStore) operations.Operation {
	t.Helper()
	op, _ := createContinuationOperation(t, store)
	op, err := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Input, next.Mission.InputSchema = json.RawMessage(`{}`), json.RawMessage(`{}`)
		next.Mission.OutputSchema = json.RawMessage(`{"type":"object","required":["summary"],"properties":{"summary":{"type":"string"}}}`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func assertTerminalRejected(t *testing.T, dir, operationID string, call func() (operations.Operation, error), want error) {
	t.Helper()
	path := filepath.Join(dir, "operations", operationID+".json")
	before := readDispatchFile(t, path)
	published, err := call()
	if err == nil || want != nil && !errors.Is(err, want) || published.ID != "" {
		t.Fatalf("terminal mutation must reject without a published snapshot: %+v, %v", published, err)
	}
	if !bytes.Equal(before, readDispatchFile(t, path)) {
		t.Fatal("rejected terminal mutation changed operation bytes or storage revision")
	}
}

func assertTerminalSnapshot(t *testing.T, expected, published operations.Operation, kind operations.EventType, started time.Time) {
	t.Helper()
	if len(published.Events) != len(expected.Events)+1 || published.UpdatedAt.Before(started) || published.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("terminal mutation did not commit one event and timestamp: %+v", published)
	}
	event := published.Events[len(published.Events)-1]
	if event.Type != kind || event.OperationID != expected.ID || event.Status != expected.Status || event.Sequence != uint64(len(expected.Events)+1) || event.TaskID != "" || event.QuestionID != "" || event.CreatedAt.Before(started) || event.CreatedAt.After(published.UpdatedAt) {
		t.Fatalf("terminal event lost its identity or commit time: %+v", event)
	}
	expected.UpdatedAt = published.UpdatedAt
	expected.Events = append(expected.Events, event)
	assertResultJSONEqual(t, published, expected)
}
