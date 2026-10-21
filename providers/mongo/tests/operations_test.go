package mongo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	mongooperations "github.com/defos-ai/commander/providers/mongo/operations"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestOperationMongoSnapshotsAndIntegrity(t *testing.T) {
	catalog, database := openOperations(t)
	ctx := t.Context()
	items, err := catalog.List(ctx)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty operations: %#v, %v", items, err)
	}
	original := newMongoOperation()
	original.Mission.OptionalActions = []string{"publish"}
	original.Mission.Rules = missions.RuleSet{
		missions.AggregateResults:  {ForActions: []string{"discover"}},
		missions.DecideNextActions: {ForActions: []string{"inspect"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	task := operations.NewTask(original.ID, "review", "", actions.NextAction{ActionName: "inspect", Input: json.RawMessage(`{ "number": 9007199254740993 }`)})
	original.Tasks = []operations.Task{task}
	question := operationQuestion("")
	original.Questions = []operations.Question{question}
	original.Preflight = &operations.Preflight{Ready: true, Version: 9007199254740993, Actions: []operations.ActionReadiness{{ActionName: "inspect", ReadyAgentIDs: []string{"review-agent-00000001"}}}}
	expected := original.Clone()

	if err := catalog.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	original.Input[0] = '!'
	original.Mission.OptionalActions[0] = "changed"
	original.Tasks[0].Input[0] = '!'
	original.Questions[0].Options[0] = "changed"
	original.Mission.Rules[missions.AggregateResults].ForActions[0] = "changed"
	original.Mission.Rules[missions.DecideNextActions].Params.DecisionBy = "changed"
	got := readMongoOperation(t, catalog, expected.ID)
	assertOperationEqual(t, got, expected)
	if !got.CreatedAt.Equal(expected.CreatedAt) || !got.Questions[0].ExpiresAt.Equal(question.ExpiresAt) {
		t.Fatal("nanosecond timestamps changed")
	}
	got.Preflight.Actions[0].ReadyAgentIDs[0] = "changed"
	got.Mission.RequiredActions[0] = "changed"
	got.Mission.OptionalActions[0] = "changed"
	got.Mission.Rules[missions.AggregateResults].ForActions[0] = "changed"
	got.Mission.Rules[missions.DecideNextActions].Params.DecisionBy = "changed"
	got.Questions[0].Options[0] = "changed"
	assertOperationEqual(t, readMongoOperation(t, catalog, expected.ID), expected)
	other := newMongoOperation()
	if err := catalog.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	items, err = catalog.List(ctx)
	if err != nil || len(items) != 2 || items[0].ID >= items[1].ID {
		t.Fatalf("operation listing: %+v, %v", items, err)
	}
	if err := catalog.Create(ctx, expected); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
	if _, err := catalog.Get(ctx, id.OperationID()); !errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("missing operation: %v", err)
	}
	if _, err := catalog.GetTask(ctx, expected.ID, id.TaskID()); !errors.Is(err, operations.ErrUnknownTask) {
		t.Fatalf("missing task: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := catalog.Cancel(cancelled, expected.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation: %v", err)
	}
	assertOperationEqual(t, readMongoOperation(t, catalog, expected.ID), expected)

	invalid := newMongoOperation()
	duplicate := expected.Tasks[0].Clone()
	duplicate.OperationID = invalid.ID
	invalid.Tasks = []operations.Task{duplicate, duplicate}
	if err := catalog.Create(ctx, invalid); err == nil {
		t.Fatal("duplicate task IDs were accepted")
	}
	if _, err := catalog.Get(ctx, invalid.ID); !errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("rejected creation left metadata behind: %v", err)
	}
	invalid = newMongoOperation()
	invalid.Input = json.RawMessage(`{"broken":`)
	if err := catalog.Create(ctx, invalid); err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	if _, err := catalog.Get(ctx, invalid.ID); !errors.Is(err, operations.ErrUnknownOperation) {
		t.Fatalf("malformed creation left records: %v", err)
	}
	if _, err := catalog.Get(ctx, "not-a-uuid"); err == nil {
		t.Fatal("invalid operation ID was accepted")
	}
	if _, err := database.Delete(ctx, "operation_tasks", bson.D{}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get(ctx, expected.ID); err == nil {
		t.Fatal("missing child records were silently omitted")
	}
	if _, err := catalog.List(ctx); err == nil {
		t.Fatal("listing hid broken operation references")
	}
}

func TestOperationMongoConcurrentQueueDispatchAndPagination(t *testing.T) {
	first, database := openOperations(t)
	second, _ := openOperations(t)
	op := createMongoOperation(t, first, newMongoOperation())
	const count = 8
	tasks := make([]operations.Task, count)
	gate, results := make(chan struct{}), make(chan error, count)
	for i := range count {
		tasks[i] = operations.NewTask(op.ID, "review", "", actions.NextAction{ActionName: "inspect", Input: json.RawMessage(fmt.Sprintf(`{"page":%d,"number":9007199254740993}`, i))})
		catalog := []operations.OperationStore{first, second}[i%2]
		go func() {
			<-gate
			_, err := catalog.QueueTask(t.Context(), op.ID, tasks[i])
			results <- err
		}()
	}
	close(gate)
	for range count {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	saved := readMongoOperation(t, first, op.ID)
	if len(saved.Tasks) != count || countOperationEvents(saved, operations.TaskAssigned) != count {
		t.Fatalf("concurrent assignments lost work: tasks=%d events=%d", len(saved.Tasks), len(saved.Events))
	}
	beforeTasks := collectionDocuments(t, database, "operation_tasks")
	unchangedTask := saved.Tasks[1]
	gate, results = make(chan struct{}), make(chan error, 2)
	for i, catalog := range []operations.OperationStore{first, second} {
		go func() {
			<-gate
			_, err := catalog.DispatchTask(t.Context(), op.ID, operations.TaskDispatch{TaskID: saved.Tasks[0].ID, AgentID: fmt.Sprintf("review-agent-%08d", i+1), StartedAt: time.Now().UTC(), DeadlineAt: time.Now().UTC().Add(time.Hour)})
			results <- err
		}()
	}
	close(gate)
	a, b := <-results, <-results
	if a != nil {
		a, b = b, a
	}
	if a != nil || !errors.Is(b, operations.ErrOperationConflict) {
		t.Fatalf("dispatch must have one owner: %v, %v", a, b)
	}
	latest := readMongoOperation(t, second, op.ID)
	assertOperationEqual(t, latest.Tasks[1], unchangedTask)
	unchanged := 0
	for _, document := range collectionDocuments(t, database, "operation_tasks") {
		for _, previous := range beforeTasks {
			if document == previous {
				unchanged++
				break
			}
		}
	}
	if unchanged != count-1 {
		t.Fatalf("dispatch rewrote unrelated tasks: %d remained unchanged", unchanged)
	}
	var listed []operations.Task
	cursor := ""
	for {
		page, err := first.ListTasks(t.Context(), op.ID, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			if page == nil {
				t.Fatal("nil empty task page")
			}
			break
		}
		listed = append(listed, page...)
		cursor = page[len(page)-1].ID
	}
	assertOperationEqual(t, listed, latest.Tasks)
	var events []operations.Event
	var sequence uint64
	for {
		page, err := second.ListEvents(t.Context(), op.ID, sequence, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			if page == nil {
				t.Fatal("nil empty event page")
			}
			break
		}
		events = append(events, page...)
		sequence = page[len(page)-1].Sequence
	}
	assertOperationEqual(t, events, latest.Events)
	for i, event := range events {
		if event.Sequence != uint64(i+1) || event.OperationID != op.ID {
			t.Fatalf("lost event sequence: %+v", events)
		}
	}
	if _, err := first.ListTasks(t.Context(), op.ID, id.TaskID(), 1); !errors.Is(err, operations.ErrUnknownTask) {
		t.Fatalf("unknown cursor: %v", err)
	}
	if _, err := first.ListEvents(t.Context(), op.ID, 0, 0); err == nil {
		t.Fatal("zero page limit accepted")
	}
	for _, task := range tasks {
		if _, err := second.QueueTask(t.Context(), op.ID, task); err != nil {
			t.Fatal(err)
		}
	}
	conflict := tasks[0].Clone()
	conflict.Input = json.RawMessage(`{"page":0,"number":9007199254740992}`)
	if _, err := first.QueueTask(t.Context(), op.ID, conflict); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("different large integer accepted as replay: %v", err)
	}
	assertOperationEqual(t, readMongoOperation(t, first, op.ID), latest)
}

func TestOperationMongoAutomaticChildrenAndDecisionRace(t *testing.T) {
	first, _ := openOperations(t)
	second, _ := openOperations(t)
	op := newMongoOperation()
	op.Mission.Rules = missions.RuleSet{missions.AggregateResults: {ForActions: []string{"discover"}}}
	op = createMongoOperation(t, first, op)
	parent := queueAndDispatch(t, first, op.ID, "discover")
	result := successfulTask(parent)
	result.Result.NextActions = []actions.NextAction{{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)}, {ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)}}
	published, err := first.FinishTask(t.Context(), op.ID, result)
	if err != nil || len(published.Tasks) != 3 || published.Tasks[0].Continuation.Status != operations.ContinuationWaiting {
		t.Fatalf("automatic fan-out: %+v, %v", published, err)
	}
	replay := result
	replay.FinishedAt = replay.FinishedAt.Add(time.Hour)
	if _, err := second.FinishTask(t.Context(), op.ID, replay); err != nil {
		t.Fatal(err)
	}
	assertOperationEqual(t, readMongoOperation(t, first, op.ID), published)
	conflict := result
	conflict.Result = &actions.ActionOutput{ActionName: "discover", Output: json.RawMessage(`{"different":true}`)}
	if _, err := first.FinishTask(t.Context(), op.ID, conflict); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("conflicting final result: %v", err)
	}
	for _, child := range published.Tasks[1:] {
		if child.ParentTaskID != parent.ID || child.Status != operations.TaskQueued {
			t.Fatalf("invalid automatic child: %+v", child)
		}
		if _, err := first.DispatchTask(t.Context(), op.ID, operationDispatch(child.ID)); err != nil {
			t.Fatal(err)
		}
	}
	gate, results := make(chan struct{}), make(chan error, 2)
	for i, catalog := range []operations.OperationStore{first, second} {
		child := readMongoOperation(t, first, op.ID).Tasks[i+1]
		go func() {
			<-gate
			_, err := catalog.FinishTask(t.Context(), op.ID, successfulTask(child))
			results <- err
		}()
	}
	close(gate)
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	settled := readMongoOperation(t, first, op.ID)
	assertOperationEqual(t, settled.Mission.Rules, op.Mission.Rules)
	decision := settled.Tasks[0].Continuation
	if decision.DecisionBy != operations.DecisionModel || decision.Status != operations.ContinuationPending || countOperationEvents(settled, operations.ContinuationReady) != 1 || countOperationEvents(settled, operations.TaskFinished) != 3 {
		t.Fatalf("last child did not settle group exactly once: %+v", settled)
	}
	gate, results = make(chan struct{}), make(chan error, 2)
	for i, catalog := range []operations.OperationStore{first, second} {
		go func() {
			<-gate
			_, err := catalog.ResolveContinuation(t.Context(), op.ID, parent.ID, decision.Version, []actions.NextAction{{ActionName: "report", Input: json.RawMessage(fmt.Sprintf(`{"choice":%d}`, i))}})
			results <- err
		}()
	}
	close(gate)
	a, b := <-results, <-results
	if a != nil {
		a, b = b, a
	}
	if a != nil || b == nil {
		t.Fatalf("stale decision was accepted: %v, %v", a, b)
	}
	final := readMongoOperation(t, second, op.ID)
	if len(final.Tasks) != 4 || final.Tasks[3].ParentTaskID != parent.ID || final.Tasks[0].Continuation.Version != decision.Version+1 || countOperationEvents(final, operations.ContinuationResolvedEvent) != 1 {
		t.Fatalf("duplicate decision branch: %+v", final)
	}
	if _, err := first.ResolveContinuation(t.Context(), op.ID, parent.ID, decision.Version, []actions.NextAction{}); err == nil {
		t.Fatal("old decision version replayed")
	}
	assertOperationEqual(t, readMongoOperation(t, first, op.ID), final)
}

func TestOperationMongoQuestionsAndExpirationRace(t *testing.T) {
	first, _ := openOperations(t)
	second, _ := openOperations(t)
	op := createMongoOperation(t, first, newMongoOperation())
	task := queueAndDispatch(t, first, op.ID, "inspect")
	op, err := first.FinishTask(t.Context(), op.ID, successfulTask(task))
	if err != nil {
		t.Fatal(err)
	}
	version := op.Tasks[0].Continuation.Version
	gate, results := make(chan struct{}), make(chan error, 2)
	for _, catalog := range []operations.OperationStore{first, second} {
		question := operationQuestion(task.ID)
		go func() { <-gate; _, err := catalog.AskQuestion(t.Context(), op.ID, question, version); results <- err }()
	}
	close(gate)
	a, b := <-results, <-results
	if a != nil {
		a, b = b, a
	}
	if a != nil || b == nil {
		t.Fatalf("linked question must have one winner: %v, %v", a, b)
	}
	waiting := readMongoOperation(t, first, op.ID)
	if len(waiting.Questions) != 1 || waiting.Status != operations.Waiting || waiting.Tasks[0].Continuation.DecisionBy != operations.DecisionUser {
		t.Fatalf("question and handoff not atomic: %+v", waiting)
	}
	question := waiting.Questions[0]
	if _, err := second.AskQuestion(t.Context(), op.ID, question, version); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("duplicate question: %v", err)
	}
	gate, results = make(chan struct{}), make(chan error, 2)
	go func() {
		<-gate
		_, err := first.AnswerQuestion(t.Context(), op.ID, question.ID, "Proceed")
		results <- err
	}()
	go func() {
		<-gate
		_, err := second.ExpireQuestions(t.Context(), op.ID, question.ExpiresAt)
		results <- err
	}()
	close(gate)
	a, b = <-results, <-results
	if a != nil && b != nil {
		t.Fatalf("neither answer nor expiration committed: %v, %v", a, b)
	}
	resolved := readMongoOperation(t, first, op.ID)
	q, continuation := resolved.Questions[0], resolved.Tasks[0].Continuation
	if q.Status != operations.QuestionAnswered && q.Status != operations.QuestionExpired {
		t.Fatalf("question not settled: %+v", q)
	}
	if (q.Status == operations.QuestionAnswered) != (q.Answer == "Proceed" && q.AnsweredAt != nil) {
		t.Fatalf("partial answer: %+v", q)
	}
	if continuation.DecisionBy != operations.DecisionModel || continuation.Status != operations.ContinuationPending || continuation.Version != version+2 || countOperationEvents(resolved, operations.QuestionAnsweredEvent)+countOperationEvents(resolved, operations.QuestionExpiredEvent) != 1 {
		t.Fatalf("question settled twice or lost continuation: %+v", resolved)
	}
	if _, err := first.ExpireQuestions(t.Context(), op.ID, question.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	assertOperationEqual(t, readMongoOperation(t, first, op.ID), resolved)
	queued := operations.NewTask(op.ID, "review", "", actions.NextAction{ActionName: "report"})
	if _, err := first.QueueTask(t.Context(), op.ID, queued); err != nil {
		t.Fatal(err)
	}
	late := operationQuestion("")
	late.AskedAt, late.ExpiresAt = time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(-time.Minute)
	if _, err := first.AskQuestion(t.Context(), op.ID, late, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := second.DispatchTask(t.Context(), op.ID, operationDispatch(queued.ID)); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("dispatch ignored pending question: %v", err)
	}
	if _, err := first.QueueTask(t.Context(), op.ID, operations.NewTask(op.ID, "review", "", actions.NextAction{ActionName: "report"})); !errors.Is(err, operations.ErrQuestionPending) {
		t.Fatalf("assignment ignored pending question: %v", err)
	}
	answered, err := second.AnswerQuestion(t.Context(), op.ID, late.ID, "Too late")
	if err != nil || answered.Questions[1].Status != operations.QuestionExpired || answered.Questions[1].Answer != "" {
		t.Fatalf("late answer did not commit expiration: %+v, %v", answered, err)
	}
	before := readMongoOperation(t, first, op.ID)
	invalid := operationQuestion("")
	invalid.Options = []string{"1", "2", "3", "4", "5"}
	if _, err := first.AskQuestion(t.Context(), op.ID, invalid, 0); err == nil {
		t.Fatal("invalid options accepted")
	}
	assertOperationEqual(t, readMongoOperation(t, second, op.ID), before)
}

func TestOperationMongoTerminalTransitionsKeepLateEvidence(t *testing.T) {
	for _, terminal := range []operations.Status{operations.Cancelled, operations.Failed, operations.Completed} {
		t.Run(string(terminal), func(t *testing.T) {
			first, _ := openOperations(t)
			second, _ := openOperations(t)
			op := createMongoOperation(t, first, newMongoOperation())
			running := queueAndDispatch(t, first, op.ID, "inspect")
			queued := operations.NewTask(op.ID, "review", "", actions.NextAction{ActionName: "report"})
			var err error
			if terminal == operations.Completed {
				if _, err := first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{}`)); err == nil {
					t.Fatal("completed an active task")
				}
				if _, err := first.FinishTask(t.Context(), op.ID, successfulTask(running)); err != nil {
					t.Fatal(err)
				}
				op, err = second.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{"score":9007199254740993}`))
			} else {
				if _, err := first.QueueTask(t.Context(), op.ID, queued); err != nil {
					t.Fatal(err)
				}
				if terminal == operations.Cancelled {
					op, err = second.Cancel(t.Context(), op.ID)
				} else {
					op, err = second.Fail(t.Context(), op.ID, "operator stopped review")
				}
			}
			if err != nil || op.Status != terminal {
				t.Fatalf("terminal transition: %+v, %v", op, err)
			}
			if terminal != operations.Completed && op.Tasks[1].Status != operations.TaskCancelled {
				t.Fatal("queued work remained runnable")
			}
			late := successfulTask(running)
			late.Result.NextActions = []actions.NextAction{{ActionName: "report"}}
			if terminal == operations.Completed {
				late = successfulTask(running)
			}
			op, err = first.FinishTask(t.Context(), op.ID, late)
			if err != nil || op.Status != terminal || op.Tasks[0].Status != operations.TaskSucceeded {
				t.Fatalf("lost late evidence or terminal status: %+v, %v", op, err)
			}
			wantTasks := 2
			if terminal == operations.Completed {
				wantTasks = 1
			}
			if len(op.Tasks) != wantTasks {
				t.Fatal("late evidence scheduled new work")
			}
			for _, mutate := range []func() (operations.Operation, error){
				func() (operations.Operation, error) { return first.Cancel(t.Context(), op.ID) },
				func() (operations.Operation, error) { return first.Fail(t.Context(), op.ID, "replace") },
				func() (operations.Operation, error) {
					return first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{}`))
				},
			} {
				if _, err := mutate(); !errors.Is(err, operations.ErrFinished) {
					t.Fatalf("terminal record overwritten: %v", err)
				}
			}
			assertOperationEqual(t, readMongoOperation(t, second, op.ID), op)
		})
	}
}

func TestOperationMongoPreflightRecoveryAndNoopWrites(t *testing.T) {
	first, database := openOperations(t)
	second, _ := openOperations(t)
	op := createMongoOperation(t, first, newMongoOperation())
	agent := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: "review-agent-00000001", Status: agents.AgentReady, Actions: []actions.Action{{Name: "inspect", Status: actions.Ready}}}}
	gate, results := make(chan struct{}), make(chan error, 2)
	for _, catalog := range []operations.OperationStore{first, second} {
		go func() {
			<-gate
			_, err := catalog.RefreshPreflight(t.Context(), op.ID, []agents.Agent{agent})
			results <- err
		}()
	}
	close(gate)
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	ready := readMongoOperation(t, first, op.ID)
	if !ready.Preflight.Ready || ready.Preflight.Version != 1 || countOperationEvents(ready, operations.PreflightUpdated) != 1 {
		t.Fatalf("duplicate preflight publication: %+v", ready)
	}
	before := allOperationDocuments(t, database)
	if _, err := second.RefreshPreflight(t.Context(), op.ID, []agents.Agent{agent}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, allOperationDocuments(t, database)) {
		t.Fatal("unchanged preflight rewrote storage")
	}
	agent.Stale = true
	if _, err := first.RefreshPreflight(t.Context(), op.ID, []agents.Agent{agent}); err != nil {
		t.Fatal(err)
	}
	task := operations.NewTask(op.ID, "review", "", actions.NextAction{ActionName: "inspect"})
	if _, err := first.QueueTask(t.Context(), op.ID, task); err != nil {
		t.Fatal(err)
	}
	blocked := readMongoOperation(t, first, op.ID)
	if _, err := second.DispatchTask(t.Context(), op.ID, operationDispatch(task.ID)); !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("dispatch ignored preflight: %v", err)
	}
	assertOperationEqual(t, readMongoOperation(t, first, op.ID), blocked)
	agent.Stale = false
	if _, err := first.RefreshPreflight(t.Context(), op.ID, []agents.Agent{agent}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.DispatchTask(t.Context(), op.ID, operationDispatch(task.ID)); err != nil {
		t.Fatal(err)
	}
	running := readMongoOperation(t, first, op.ID)
	interrupted := operations.TaskResult{TaskID: task.ID, AgentID: running.Tasks[0].AgentID, Status: operations.TaskInterrupted, Error: "receipt timed out", FinishedAt: time.Now().UTC()}
	if _, err := first.FinishTask(t.Context(), op.ID, interrupted); err != nil {
		t.Fatal(err)
	}
	prior := readMongoOperation(t, first, op.ID)
	recovered, err := second.Recover(t.Context(), op.ID, time.Now().UTC())
	if err != nil || len(recovered.Tasks) != 1 || countOperationEvents(recovered, operations.OperationRecovered) != 1 {
		t.Fatalf("recovery duplicated or dispatched tasks: %+v, %v", recovered, err)
	}
	assertOperationEqual(t, recovered.Tasks, prior.Tasks)
	if _, err := first.FinishTask(t.Context(), op.ID, successfulTask(running.Tasks[0])); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Complete(t.Context(), op.ID, id.ReportID(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	before = allOperationDocuments(t, database)
	if _, err := second.Recover(t.Context(), op.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, allOperationDocuments(t, database)) {
		t.Fatal("terminal recovery without outstanding work rewrote storage")
	}
}

func TestOperationMongoRejectedMutationsLeaveAllCollectionsUnchanged(t *testing.T) {
	catalog, database := openOperations(t)
	op := createMongoOperation(t, catalog, newMongoOperation())
	task := queueAndDispatch(t, catalog, op.ID, "inspect")
	before := allOperationDocuments(t, database)
	for name, mutate := range map[string]func() (operations.Operation, error){
		"bad output": func() (operations.Operation, error) {
			result := successfulTask(task)
			result.Result.Output = json.RawMessage(`[]`)
			return catalog.FinishTask(t.Context(), op.ID, result)
		},
		"wrong agent": func() (operations.Operation, error) {
			result := successfulTask(task)
			result.AgentID = "review-agent-00000002"
			return catalog.FinishTask(t.Context(), op.ID, result)
		},
		"missing task": func() (operations.Operation, error) {
			result := successfulTask(task)
			result.TaskID = id.TaskID()
			return catalog.FinishTask(t.Context(), op.ID, result)
		},
		"missing continuation": func() (operations.Operation, error) {
			return catalog.ResolveContinuation(t.Context(), op.ID, task.ID, 1, []actions.NextAction{{ActionName: "report"}})
		},
		"zero recovery cutoff": func() (operations.Operation, error) { return catalog.Recover(t.Context(), op.ID, time.Time{}) },
		"blank failure":        func() (operations.Operation, error) { return catalog.Fail(t.Context(), op.ID, " ") },
	} {
		t.Run(name, func(t *testing.T) {
			published, err := mutate()
			if err == nil || published.ID != "" {
				t.Fatalf("invalid mutation exposed a snapshot: %+v, %v", published, err)
			}
			if !reflect.DeepEqual(before, allOperationDocuments(t, database)) {
				t.Fatal("rejected mutation changed metadata or child records")
			}
		})
	}
}

func openOperations(t *testing.T) (*mongooperations.OperationMongoStore, *store.MongoStore) {
	t.Helper()
	database := openStore(t, "operations")
	clean := func(ctx context.Context) {
		for _, collection := range []string{"operation_tasks", "operation_questions", "operation_events"} {
			if _, err := database.Delete(ctx, collection, bson.D{}); err != nil {
				t.Error(err)
			}
		}
	}
	clean(t.Context())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		clean(ctx)
	})
	catalog, err := mongooperations.NewOperationMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, database
}

func newMongoOperation() operations.Operation {
	created := time.Date(2026, time.September, 30, 12, 0, 0, 123456789, time.UTC)
	op := operations.Operation{ID: id.OperationID(), Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Mission: missions.Mission{Name: "review", Objective: "Inspect pages", RequiredActions: []string{"inspect"}, Steps: []missions.Step{{ID: "review"}}, InputSchema: json.RawMessage(`{ "type":"object", "x-example":9007199254740993 }`), OutputSchema: json.RawMessage(`{"type":"object"}`)},
		Input:   json.RawMessage(`{ "number":9007199254740993 }`), Tasks: []operations.Task{}, Questions: []operations.Question{}, Events: []operations.Event{}}
	op.Events = append(op.Events, operations.Event{Sequence: 1, OperationID: op.ID, Type: operations.OperationStarted, Status: operations.Planning, CreatedAt: created})
	return op
}

func createMongoOperation(t *testing.T, catalog operations.OperationStore, op operations.Operation) operations.Operation {
	t.Helper()
	if err := catalog.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return readMongoOperation(t, catalog, op.ID)
}

func readMongoOperation(t *testing.T, catalog operations.OperationStore, operationID string) operations.Operation {
	t.Helper()
	op, err := catalog.Get(t.Context(), operationID)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func queueAndDispatch(t *testing.T, catalog operations.OperationStore, operationID, action string) operations.Task {
	t.Helper()
	task := operations.NewTask(operationID, "review", "", actions.NextAction{ActionName: action, Input: json.RawMessage(`{"value":9007199254740993}`)})
	if _, err := catalog.QueueTask(t.Context(), operationID, task); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.DispatchTask(t.Context(), operationID, operationDispatch(task.ID)); err != nil {
		t.Fatal(err)
	}
	saved, err := catalog.GetTask(t.Context(), operationID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func operationDispatch(taskID string) operations.TaskDispatch {
	started := time.Now().UTC()
	return operations.TaskDispatch{TaskID: taskID, AgentID: "review-agent-00000001", StartedAt: started, DeadlineAt: started.Add(time.Hour)}
}

func successfulTask(task operations.Task) operations.TaskResult {
	return operations.TaskResult{TaskID: task.ID, AgentID: task.AgentID, Status: operations.TaskSucceeded, FinishedAt: time.Now().UTC(), Result: &actions.ActionOutput{ActionName: task.ActionName, Output: json.RawMessage(`{"number":9007199254740993}`)}}
}

func operationQuestion(taskID string) operations.Question {
	asked := time.Now().UTC()
	return operations.Question{ID: id.QuestionID(), TaskID: taskID, Status: operations.QuestionPending, Text: "Continue the review?", Options: []string{"Continue", "Stop"}, AskedAt: asked, ExpiresAt: asked.Add(time.Hour)}
}

func countOperationEvents(op operations.Operation, kind operations.EventType) int {
	count := 0
	for _, event := range op.Events {
		if event.Type == kind {
			count++
		}
	}
	return count
}

func assertOperationEqual(t *testing.T, got, want any) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("snapshot differs:\n got %s\nwant %s", a, b)
	}
}

func collectionDocuments(t *testing.T, database *store.MongoStore, collection string) []string {
	t.Helper()
	var documents []bson.Raw
	if err := database.Find(t.Context(), collection, bson.D{}, &documents); err != nil {
		t.Fatal(err)
	}
	encoded := make([]string, len(documents))
	for i, document := range documents {
		encoded[i] = string(document)
	}
	sort.Strings(encoded)
	return encoded
}

func allOperationDocuments(t *testing.T, database *store.MongoStore) map[string][]string {
	t.Helper()
	result := make(map[string][]string)
	for _, collection := range []string{"operations", "operation_tasks", "operation_questions", "operation_events"} {
		result[collection] = collectionDocuments(t, database, collection)
	}
	return result
}

func TestOperationMongoQueriesUseOrderedCursors(t *testing.T) {
	catalog, database := openOperations(t)
	op := newMongoOperation()
	op.Preflight = &operations.Preflight{Version: math.MaxUint64}
	for range 2 {
		op.Tasks = append(op.Tasks, operations.NewTask(op.ID, "review", "", actions.NextAction{ActionName: "inspect"}))
	}
	op.Events = nil
	for _, sequence := range []uint64{math.MaxUint64, 2, 10} {
		op.Events = append(op.Events, operations.Event{OperationID: op.ID, Sequence: sequence, Type: operations.TaskAssigned})
	}
	if err := catalog.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	assertOperationEqual(t, readMongoOperation(t, catalog, op.ID), op)
	page, err := catalog.ListEvents(t.Context(), op.ID, 2, 1)
	if err != nil || len(page) != 1 || page[0].Sequence != 10 {
		t.Fatalf("event sequence ordering: %+v, %v", page, err)
	}
	page, err = catalog.ListEvents(t.Context(), op.ID, 10, 2)
	if err != nil || len(page) != 1 || page[0].Sequence != math.MaxUint64 {
		t.Fatalf("unsigned event sequence: %+v, %v", page, err)
	}
	page, err = catalog.ListEvents(t.Context(), op.ID, math.MaxUint64, 1)
	if err != nil || page == nil || len(page) != 0 {
		t.Fatalf("final event cursor: %+v, %v", page, err)
	}

	if _, err := database.Update(t.Context(), "operation_tasks", bson.M{"_id": op.ID + "/" + op.Tasks[0].ID}, bson.M{"$set": bson.M{"data": []byte("broken")}}); err != nil {
		t.Fatal(err)
	}
	task, err := catalog.GetTask(t.Context(), op.ID, op.Tasks[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	assertOperationEqual(t, task, op.Tasks[1])
	tasks, err := catalog.ListTasks(t.Context(), op.ID, op.Tasks[0].ID, 1)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("task cursor loaded an unrelated body: %+v, %v", tasks, err)
	}
	assertOperationEqual(t, tasks[0], op.Tasks[1])
	if _, err := catalog.GetTask(t.Context(), op.ID, op.Tasks[0].ID); err == nil {
		t.Fatal("reading a corrupt task succeeded")
	}
	if _, err := catalog.Get(t.Context(), op.ID); err == nil {
		t.Fatal("aggregate read ignored a corrupt task")
	}
}
