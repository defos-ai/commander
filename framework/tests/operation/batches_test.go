package operation_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/operation"
)

func TestAggregationBatchLateOutcomeRetainsItsOriginalGroup(t *testing.T) {
	op := fixtures.AggregationReview(t)
	parentID := op.Tasks[0].ID
	if err := op.ResolveContinuation(parentID, op.Tasks[0].Continuation.Version,
		[]actions.NextAction{{ActionName: "inspect"}, {ActionName: "inspect"}}); err != nil {
		t.Fatal(err)
	}
	firstBatch := op.Tasks[1].BatchID
	assertBatchEvents(t, op, firstBatch, parentID, 1, 0)
	for i, status := range []operations.TaskStatus{operations.TaskFailed, operations.TaskInterrupted} {
		task := op.Tasks[i+1]
		now := time.Now().UTC()
		if err := op.DispatchTask(operations.TaskDispatch{TaskID: task.ID, AgentID: fixtures.AgentID,
			StartedAt: now, DeadlineAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if _, err := op.FinishTask(operations.TaskResult{TaskID: task.ID, AgentID: fixtures.AgentID,
			Status: status, Error: "Producer did not return a successful result", FinishedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if c := op.Tasks[0].Continuation; c.Status != operations.ContinuationPending || c.DecisionBy != operations.DecisionModel {
		t.Fatalf("unknown batch did not become reviewable: %+v", c)
	}
	assertBatchEvents(t, op, firstBatch, parentID, 1, 0)

	if err := op.ResolveContinuation(parentID, op.Tasks[0].Continuation.Version,
		[]actions.NextAction{{ActionName: "inspect"}}); err != nil {
		t.Fatal(err)
	}
	secondBatch := op.Tasks[3].BatchID
	if secondBatch == firstBatch || op.Tasks[1].BatchID != firstBatch || op.Tasks[2].BatchID != firstBatch {
		t.Fatalf("new review decision replaced the old group: %+v", op.Tasks)
	}
	beforeRecovery := op.Clone()
	if _, err := op.Recover(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(op.Tasks, beforeRecovery.Tasks) {
		t.Fatalf("recovery changed batch membership or unknown work: %+v", op.Tasks)
	}
	assertBatchEvents(t, op, firstBatch, parentID, 1, 0)
	assertBatchEvents(t, op, secondBatch, parentID, 1, 0)

	output := fixtures.Output("inspect")
	late := operations.TaskResult{TaskID: op.Tasks[2].ID, AgentID: fixtures.AgentID,
		Status: operations.TaskSucceeded, Result: &output, FinishedAt: time.Now().UTC()}
	if changed, err := op.FinishTask(late); err != nil || !changed {
		t.Fatalf("late known outcome was not accepted: changed=%v, err=%v", changed, err)
	}

	assertBatchEvents(t, op, firstBatch, parentID, 1, 1)
	assertBatchEvents(t, op, secondBatch, parentID, 1, 0)
	if c := op.Tasks[0].Continuation; c.Status != operations.ContinuationWaiting || !reflect.DeepEqual(c.CreatedTaskIDs, []string{op.Tasks[3].ID}) {
		t.Fatalf("old batch completion changed the new decision: %+v", c)
	}
	beforeReplay := op.Clone()
	if changed, err := op.FinishTask(late); err != nil || changed || !reflect.DeepEqual(op.Clone(), beforeReplay) {
		t.Fatalf("result replay duplicated batch completion: changed=%v, err=%v", changed, err)
	}
	if _, err := op.Recover(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	assertBatchEvents(t, op, firstBatch, parentID, 1, 1)
}

func TestAggregationBatchCancellationWaitsForDispatchedWork(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		name := "all queued"
		if dispatched {
			name = "one producer running"
		}
		t.Run(name, func(t *testing.T) {
			op := fixtures.AggregationReview(t)
			parentID := op.Tasks[0].ID
			if err := op.ResolveContinuation(parentID, op.Tasks[0].Continuation.Version,
				[]actions.NextAction{{ActionName: "inspect"}, {ActionName: "inspect"}}); err != nil {
				t.Fatal(err)
			}
			batchID := op.Tasks[1].BatchID
			if dispatched {
				now := time.Now().UTC()
				if err := op.DispatchTask(operations.TaskDispatch{TaskID: op.Tasks[1].ID, AgentID: fixtures.AgentID,
					StartedAt: now, DeadlineAt: now.Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}

			if err := op.Cancel(); err != nil {
				t.Fatal(err)
			}

			finished := 1
			if dispatched {
				finished = 0
			}
			assertBatchEvents(t, op, batchID, parentID, 1, finished)
			if op.Status != operations.Cancelled || op.Tasks[2].Status != operations.TaskCancelled {
				t.Fatalf("operation cancellation lost queued producer state: %+v", op)
			}
			if _, err := op.Recover(time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			assertBatchEvents(t, op, batchID, parentID, 1, finished)
			if dispatched {
				output := fixtures.Output("inspect")
				if _, err := op.FinishTask(operations.TaskResult{TaskID: op.Tasks[1].ID, AgentID: fixtures.AgentID,
					Status: operations.TaskSucceeded, Result: &output, FinishedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
				assertBatchEvents(t, op, batchID, parentID, 1, 1)
				if op.Status != operations.Cancelled {
					t.Fatalf("late batch outcome changed terminal operation status: %s", op.Status)
				}
			}
		})
	}
}

func TestAggregationBatchRecoveryRejectsInvalidBoundariesWithoutMutation(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		mutate func(*testing.T, *operations.Operation)
	}{
		{name: "orphan membership", mutate: func(_ *testing.T, op *operations.Operation) {
			op.Tasks[1].BatchID = id.New()
		}},
		{name: "missing start", mutate: func(_ *testing.T, op *operations.Operation) {
			op.Events = slices.DeleteFunc(op.Events, func(event operations.Event) bool { return event.Type == operations.TaskBatchStarted })
		}},
		{name: "unrelated parent", mutate: func(_ *testing.T, op *operations.Operation) {
			op.Tasks[1].ParentTaskID = id.TaskID()
		}},
		{name: "parent belongs to its own batch", mutate: func(_ *testing.T, op *operations.Operation) {
			op.Tasks[0].BatchID = op.Tasks[1].BatchID
		}},
		{name: "duplicate start", mutate: func(_ *testing.T, op *operations.Operation) {
			op.AppendEvent(operations.TaskBatchStarted, op.Tasks[0].ID, "", "Duplicate start")
			op.Events[len(op.Events)-1].BatchID = op.Tasks[1].BatchID
		}},
		{name: "queued member marked finished", mutate: func(_ *testing.T, op *operations.Operation) {
			op.AppendEvent(operations.TaskBatchFinished, op.Tasks[0].ID, "", "Premature completion")
			op.Events[len(op.Events)-1].BatchID = op.Tasks[1].BatchID
		}},
		{name: "interrupted member marked finished", mutate: func(t *testing.T, op *operations.Operation) {
			now := time.Now().UTC()
			if err := op.DispatchTask(operations.TaskDispatch{TaskID: op.Tasks[1].ID, AgentID: fixtures.AgentID,
				StartedAt: now, DeadlineAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if _, err := op.FinishTask(operations.TaskResult{TaskID: op.Tasks[1].ID, AgentID: fixtures.AgentID,
				Status: operations.TaskInterrupted, Error: "Outcome unknown", FinishedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			op.AppendEvent(operations.TaskBatchFinished, op.Tasks[0].ID, "", "Premature completion")
			op.Events[len(op.Events)-1].BatchID = op.Tasks[1].BatchID
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			op := fixtures.AggregationReview(t)
			if err := op.ResolveContinuation(op.Tasks[0].ID, op.Tasks[0].Continuation.Version,
				[]actions.NextAction{{ActionName: "inspect"}}); err != nil {
				t.Fatal(err)
			}
			scenario.mutate(t, &op)
			before := op.Clone()

			changed, err := op.Recover(time.Now().UTC())

			if err == nil || changed || !reflect.DeepEqual(op.Clone(), before) {
				t.Fatalf("invalid batch recovery changed saved evidence: changed=%v, error=%v, operation=%+v", changed, err, op)
			}
		})
	}
}

func assertBatchEvents(t *testing.T, op operations.Operation, batchID, parentID string, starts, finishes int) {
	t.Helper()
	if !id.Valid(batchID) {
		t.Fatalf("batch has no stable UUID: %q", batchID)
	}
	started, finished := 0, 0
	var startSequence, finishSequence, resultSequence uint64
	for _, event := range op.Events {
		if event.Type == operations.TaskFinished {
			for _, task := range op.Tasks {
				if task.BatchID == batchID && task.ID == event.TaskID {
					resultSequence = event.Sequence
				}
			}
		}
		if event.BatchID != batchID {
			continue
		}
		if event.TaskID != parentID || event.OperationID != op.ID || event.CreatedAt.IsZero() {
			t.Fatalf("batch event lost its saved scope or timestamp: %+v", event)
		}
		switch event.Type {
		case operations.TaskBatchStarted:
			started++
			startSequence = event.Sequence
		case operations.TaskBatchFinished:
			finished++
			finishSequence = event.Sequence
		}
	}
	if started != starts || finished != finishes {
		t.Fatalf("batch %s boundaries: started=%d finished=%d; want %d/%d; events=%+v", batchID, started, finished, starts, finishes, op.Events)
	}
	if finished != 0 && (finishSequence <= startSequence || finishSequence <= resultSequence) {
		t.Fatalf("batch finish preceded accepted member outcomes: start=%d result=%d finish=%d", startSequence, resultSequence, finishSequence)
	}
}
