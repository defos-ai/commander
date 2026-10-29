package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestOperationCompletionPublishesReportAndDecisionsTogether(t *testing.T) {
	dir := t.TempDir()
	first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
	op := fixtures.CreateCompletionOperation(t, first)
	latest, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Mission.Completion = &missions.CompletionPolicy{DecisionBy: missions.Operator}
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

	published, err := first.Complete(t.Context(), op.ID, reportID, output, operations.CompletionOperator)

	if err != nil {
		t.Fatal(err)
	}
	expected := latest.Clone()
	expected.Status, expected.ReportID, expected.Output = operations.Completed, reportID, bytes.Clone(output)
	expected.CompletedBy = operations.CompletionOperator
	expected.Tasks[0].Continuation.Status = operations.ContinuationResolved
	expected.Tasks[0].Continuation.Version++
	assertTerminalSnapshot(t, expected, published, operations.OperationCompleted, started)
	output[0] = '['
	saved, err := fixtures.NewDispatchStore(t, dir).Get(t.Context(), op.ID)
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

func TestOperationCompletionProposalPersistsWithoutCompleting(t *testing.T) {
	dir := t.TempDir()
	first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
	op := fixtures.CreateCompletionOperation(t, first)
	op, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
		next.Mission.Completion = &missions.CompletionPolicy{DecisionBy: missions.Operator}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := fixtures.CompletionProposal()

	published, err := first.ProposeCompletion(t.Context(), op.ID, proposal)

	if err != nil || published.Status != op.Status || published.ReportID != "" || published.Output != nil || published.CompletedBy != "" {
		t.Fatalf("proposal completed the operation or wrote final report fields: %+v, %v", published, err)
	}
	assertResultJSONEqual(t, published.Tasks, op.Tasks)
	assertResultJSONEqual(t, published.Questions, op.Questions)
	if published.CompletionProposal == nil || len(published.Events) != len(op.Events)+1 ||
		published.Events[len(published.Events)-1].Type != operations.CompletionProposed {
		t.Fatal("proposal and review event did not commit together")
	}
	expected := proposal.Clone()
	proposal.Output[0], published.CompletionProposal.Output[0] = '[', '['
	published.CompletionProposal.ProposedBy = operations.CompletionOperator

	saved, err := fixtures.NewDispatchStore(t, dir).Get(t.Context(), op.ID)

	if err != nil || saved.CompletionProposal == nil {
		t.Fatal("proposal was lost across reopening the provider")
	}
	assertResultJSONEqual(t, saved.CompletionProposal, expected)
	if !bytes.Contains(saved.CompletionProposal.Output, []byte("9007199254740993")) ||
		!bytes.Contains(saved.CompletionProposal.Output, []byte(`\u0061\/b`)) {
		t.Fatal("proposal persistence changed number precision or string escapes")
	}
	path := filepath.Join(dir, "operations", op.ID+".json")
	before := fixtures.ReadDispatchFile(t, path)

	replayed, err := second.ProposeCompletion(t.Context(), op.ID, expected)

	if err != nil || !bytes.Equal(before, fixtures.ReadDispatchFile(t, path)) {
		t.Fatal("identical proposal replay rewrote the saved operation")
	}
	assertResultJSONEqual(t, replayed, saved)
	conflicting := expected.Clone()
	conflicting.Output = json.RawMessage(`{"summary":"Changed under the same ID"}`)
	assertTerminalRejected(t, dir, op.ID, func() (operations.Operation, error) {
		return first.ProposeCompletion(t.Context(), op.ID, conflicting)
	}, operations.ErrOperationConflict)
	replacement := expected.Clone()
	replacement.ID, replacement.Output = id.New(), json.RawMessage(`{"summary":"Updated proposal"}`)

	revised, err := second.ProposeCompletion(t.Context(), op.ID, replacement)

	if err != nil || revised.CompletionProposal == nil || revised.CompletionProposal.ID != replacement.ID ||
		len(revised.Events) != len(saved.Events)+1 {
		t.Fatalf("a new proposal did not replace the review copy: %+v, %v", revised, err)
	}

	finished, err := first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"summary":"Operator edited and accepted"}`), operations.CompletionOperator)

	if err != nil || finished.Status != operations.Completed {
		t.Fatalf("proposal prevented explicit operator completion: %+v, %v", finished, err)
	}
	assertResultJSONEqual(t, finished.CompletionProposal, replacement)
	assertTerminalRejected(t, dir, op.ID, func() (operations.Operation, error) {
		return second.ProposeCompletion(t.Context(), op.ID, fixtures.CompletionProposal())
	}, operations.ErrFinished)
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
		{"operator completion policy", func(op *operations.Operation) {
			op.Mission.Completion = &missions.CompletionPolicy{DecisionBy: missions.Operator}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
			op := fixtures.CreateCompletionOperation(t, first)
			if _, err := second.Update(t.Context(), op.ID, func(next *operations.Operation) error {
				tc.change(next)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "operations", op.ID+".json")
			before := fixtures.ReadDispatchFile(t, path)

			published, err := first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"summary":"done"}`), operations.CompletionModel)

			if err == nil || published.ID != "" {
				t.Fatalf("completion accepted stale eligibility: %+v, %v", published, err)
			}
			if tc.name == "pending question" && !errors.Is(err, operations.ErrQuestionPending) {
				t.Fatalf("pending question lost its error identity: %v", err)
			}
			if tc.name == "operator completion policy" && !errors.Is(err, operations.ErrCompletionOperatorRequired) {
				t.Fatalf("operator completion policy lost its error identity: %v", err)
			}
			if !bytes.Equal(before, fixtures.ReadDispatchFile(t, path)) {
				t.Fatal("rejected completion changed the latest snapshot or storage revision")
			}
			if tc.name != "operator completion policy" {
				published, err := first.ProposeCompletion(t.Context(), op.ID, fixtures.CompletionProposal())

				if err == nil || published.ID != "" || !bytes.Equal(before, fixtures.ReadDispatchFile(t, path)) {
					t.Fatal("proposal bypassed the latest saved completion eligibility")
				}
			}
		})
	}
}

func TestOperationTerminalWritesRejectInvalidInputsAndTerminalState(t *testing.T) {
	dir := t.TempDir()
	store := fixtures.NewDispatchStore(t, dir)
	op := fixtures.CreateCompletionOperation(t, store)
	complete := func(reportID string, output json.RawMessage) func() (operations.Operation, error) {
		return func() (operations.Operation, error) {
			return store.Complete(t.Context(), op.ID, reportID, output, operations.CompletionModel)
		}
	}
	fail := func(reason string) func() (operations.Operation, error) {
		return func() (operations.Operation, error) { return store.Fail(t.Context(), op.ID, reason) }
	}
	validReportID, validOutput := id.ReportID(), json.RawMessage(`{"summary":"done"}`)
	propose := func(change func(*operations.CompletionProposal)) func() (operations.Operation, error) {
		return func() (operations.Operation, error) {
			proposal := fixtures.CompletionProposal()
			change(&proposal)
			return store.ProposeCompletion(t.Context(), op.ID, proposal)
		}
	}
	for _, tc := range []struct {
		name string
		call func() (operations.Operation, error)
	}{
		{"empty report ID", complete("", validOutput)},
		{"noncanonical report ID", complete(strings.ToUpper("123e4567-e89b-12d3-a456-426614174000"), validOutput)},
		{"malformed output", complete(validReportID, json.RawMessage(`{"summary":`))},
		{"invalid output", complete(validReportID, json.RawMessage(`{}`))},
		{"missing completion origin", func() (operations.Operation, error) {
			return store.Complete(t.Context(), op.ID, validReportID, validOutput, "")
		}},
		{"unknown completion origin", func() (operations.Operation, error) {
			return store.Complete(t.Context(), op.ID, validReportID, validOutput, "service")
		}},
		{"invalid proposal ID", propose(func(p *operations.CompletionProposal) { p.ID = "" })},
		{"invalid proposal JSON", propose(func(p *operations.CompletionProposal) { p.Output = json.RawMessage(`{`) })},
		{"invalid proposal output", propose(func(p *operations.CompletionProposal) { p.Output = json.RawMessage(`{}`) })},
		{"missing proposal origin", propose(func(p *operations.CompletionProposal) { p.ProposedBy = "" })},
		{"missing proposal time", propose(func(p *operations.CompletionProposal) { p.ProposedAt = time.Time{} })},
		{"empty failure", fail("")},
		{"blank failure", fail(" \t\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTerminalRejected(t, dir, op.ID, tc.call, nil)
		})
	}
	for _, status := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(status), func(t *testing.T) {
			if _, err := fixtures.NewDispatchStore(t, dir).Update(t.Context(), op.ID, func(next *operations.Operation) error {
				next.Status = status
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertTerminalRejected(t, dir, op.ID, complete(validReportID, validOutput), operations.ErrFinished)
			assertTerminalRejected(t, dir, op.ID, fail("Cannot continue"), operations.ErrFinished)
			assertTerminalRejected(t, dir, op.ID, propose(func(*operations.CompletionProposal) {}), operations.ErrFinished)
		})
	}
}

func TestOperationCompetingTerminalWritesPublishOneWinner(t *testing.T) {
	for _, competingStatus := range []operations.Status{operations.Completed, operations.Failed, operations.Cancelled} {
		t.Run(string(competingStatus), func(t *testing.T) {
			dir := t.TempDir()
			first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
			op := fixtures.CreateCompletionOperation(t, first)
			compete := func() (operations.Operation, error) {
				switch competingStatus {
				case operations.Completed:
					return second.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"summary":"second"}`), operations.CompletionModel)
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
					return first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"summary":"first"}`), operations.CompletionModel)
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
			saved, err := fixtures.NewDispatchStore(t, dir).Get(t.Context(), op.ID)
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
	first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
	op, result := fixtures.CreateResultOperation(t, first)
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
	saved, err := fixtures.NewDispatchStore(t, dir).Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultJSONEqual(t, saved, published)
}

func TestOperationFailureSerializesWithAutomaticFollowupsAndLateResults(t *testing.T) {
	for _, order := range []string{"failure first", "result first", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			dir := t.TempDir()
			first, second := fixtures.NewDispatchStore(t, dir), fixtures.NewDispatchStore(t, dir)
			op, request := fixtures.CreateResultOperation(t, first)
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
			saved, err := fixtures.NewDispatchStore(t, dir).Get(t.Context(), op.ID)
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

func assertTerminalRejected(t *testing.T, dir, operationID string, call func() (operations.Operation, error), want error) {
	t.Helper()
	path := filepath.Join(dir, "operations", operationID+".json")
	before := fixtures.ReadDispatchFile(t, path)
	published, err := call()
	if err == nil || want != nil && !errors.Is(err, want) || published.ID != "" {
		t.Fatalf("terminal mutation must reject without a published snapshot: %+v, %v", published, err)
	}
	if !bytes.Equal(before, fixtures.ReadDispatchFile(t, path)) {
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
