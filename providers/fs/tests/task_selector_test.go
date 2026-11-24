package providers_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestOperationTaskSelectorSurvivesQueueDispatchAndReopen(t *testing.T) {
	dir := t.TempDir()
	store := fixtures.NewDispatchStore(t, dir)
	op, unselected := fixtures.CreateQueueOperation(t, store)
	if _, err := store.QueueTask(t.Context(), op.ID, unselected); err != nil {
		t.Fatal(err)
	}
	task := operations.NewTask(op.ID, "", actions.NextAction{ActionName: "inspect"})
	task.Selector = &agents.Selector{Type: "review-agent", Hostname: "k8s-prod"}
	assignment := task.Clone()

	queued, err := store.QueueTask(t.Context(), op.ID, task)

	if err != nil || len(queued.Tasks) != 2 || queued.Tasks[0].Selector != nil ||
		!reflect.DeepEqual(queued.Tasks[1].Selector, assignment.Selector) {
		t.Fatalf("queue lost targeted or untargeted assignment: %+v, %v", queued, err)
	}
	task.Selector.Hostname = "changed-input"
	queued.Tasks[1].Selector.Hostname = "changed-result"
	reopened := fixtures.NewDispatchStore(t, dir)
	saved, err := reopened.Get(t.Context(), op.ID)
	if err != nil || saved.Tasks[0].Selector != nil || !reflect.DeepEqual(saved.Tasks[1], assignment) {
		t.Fatalf("reopen lost selector or retained a caller-owned pointer: %+v, %v", saved, err)
	}
	single, err := reopened.GetTask(t.Context(), op.ID, assignment.ID)
	if err != nil || !reflect.DeepEqual(single, assignment) {
		t.Fatalf("task query lost selector: %+v, %v", single, err)
	}
	page, err := reopened.ListTasks(t.Context(), op.ID, unselected.ID, 1)
	if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0], assignment) {
		t.Fatalf("task pagination lost selector: %+v, %v", page, err)
	}
	single.Selector.Hostname = "changed-query"
	page[0].Selector.Hostname = "changed-page"
	started := time.Now().UTC()
	dispatched, err := reopened.DispatchTask(t.Context(), op.ID, operations.TaskDispatch{
		TaskID: assignment.ID, AgentID: "review-agent-00000001", StartedAt: started, DeadlineAt: started.Add(time.Minute),
	})
	if err != nil || !reflect.DeepEqual(dispatched.Tasks[1].Selector, assignment.Selector) {
		t.Fatalf("dispatch lost the requested selector: %+v, %v", dispatched, err)
	}
	retried, err := reopened.QueueTask(t.Context(), op.ID, assignment)
	if err != nil || !reflect.DeepEqual(retried, dispatched) {
		t.Fatalf("same assignment changed the dispatched snapshot: %+v, %v", retried, err)
	}
	for _, replacement := range []*agents.Selector{nil, {Type: "review-agent", Hostname: "native-host"}} {
		changed := assignment.Clone()
		changed.Selector = replacement
		if _, err := reopened.QueueTask(t.Context(), op.ID, changed); !errors.Is(err, operations.ErrOperationConflict) {
			t.Fatalf("changed selector did not conflict with the saved assignment: %v", err)
		}
	}
	recovered, err := fixtures.NewDispatchStore(t, dir).Recover(t.Context(), op.ID, started.Add(time.Second))
	if err != nil || !reflect.DeepEqual(recovered.Tasks[1], dispatched.Tasks[1]) {
		t.Fatalf("recovery lost the saved placement or dispatch: %+v, %v", recovered, err)
	}
	for index := range recovered.Tasks {
		invalidRecovery := recovered.Clone()
		invalidRecovery.Tasks[index].Selector = &agents.Selector{}
		before := invalidRecovery.Clone()
		changed, err := invalidRecovery.Recover(started.Add(2 * time.Second))
		if changed || err == nil || !reflect.DeepEqual(invalidRecovery, before) {
			t.Fatalf("invalid recovered selector changed task %d state: changed=%v error=%v", index, changed, err)
		}
	}
}

func TestOperationTaskSelectorRejectsInvalidWritesWithoutChangingSavedState(t *testing.T) {
	for _, selector := range []agents.Selector{{}, {Hostname: "k8s prod"}, {Hostname: string([]byte{0xff})}} {
		dir := t.TempDir()
		store := fixtures.NewDispatchStore(t, dir)
		op, task := fixtures.CreateQueueOperation(t, store)
		task.Selector = &selector

		_, queueErr := store.QueueTask(t.Context(), op.ID, task)
		_, updateErr := store.Update(t.Context(), op.ID, func(next *operations.Operation) error {
			next.Tasks = []operations.Task{task}
			return nil
		})
		if queueErr == nil || updateErr == nil {
			t.Fatalf("invalid selector was persisted: selector=%+v queue=%v update=%v", selector, queueErr, updateErr)
		}
		saved, err := fixtures.NewDispatchStore(t, dir).Get(t.Context(), op.ID)
		if err != nil || !reflect.DeepEqual(saved, op) {
			t.Fatalf("rejected selector changed the original snapshot: %+v, %v", saved, err)
		}
		freshDir := t.TempDir()
		fresh := fixtures.NewDispatchStore(t, freshDir)
		invalid := op.Clone()
		invalid.Tasks = []operations.Task{task}
		if err := fresh.Create(t.Context(), invalid); err == nil {
			t.Fatalf("invalid selector was accepted at creation: %+v", selector)
		}
		if _, err := fresh.Get(t.Context(), op.ID); !errors.Is(err, operations.ErrUnknownOperation) {
			t.Fatalf("rejected creation left a saved operation: %v", err)
		}
	}
}

func TestOperationTaskSelectorRejectsMalformedStoredSelectors(t *testing.T) {
	for _, raw := range []string{`{}`, `{"hostname":"k8s prod"}`, `{"hostname":"k8s-prod","unknown":true}`} {
		dir := t.TempDir()
		store := fixtures.NewDispatchStore(t, dir)
		op, task := fixtures.CreateQueueOperation(t, store)
		if _, err := store.QueueTask(t.Context(), op.ID, task); err != nil {
			t.Fatal(err)
		}
		fixtures.SetTaskSelectorRecord(t, dir, op.ID, task.ID, json.RawMessage(raw))

		_, operationErr := store.Get(t.Context(), op.ID)
		_, taskErr := store.GetTask(t.Context(), op.ID, task.ID)
		_, pageErr := store.ListTasks(t.Context(), op.ID, "", 1)

		if operationErr == nil || taskErr == nil || pageErr == nil {
			t.Fatalf("malformed selector reached a read result: selector=%s operation=%v task=%v page=%v", raw, operationErr, taskErr, pageErr)
		}
	}
}
