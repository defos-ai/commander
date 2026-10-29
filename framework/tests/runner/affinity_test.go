package runner_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/runner"
)

func TestOperationAffinityConstrainsOnlyMatchingDispatches(t *testing.T) {
	op := fixtures.AffinityOperation()
	assertAffinity(t, op, "")
	dispatchAffinityTask(t, &op, 2, "builder-agent-bbbbbbbb")
	assertAffinity(t, op, "")
	dispatchAffinityTask(t, &op, 0, "builder-agent-aaaaaaaa")
	assertAffinity(t, op, "builder-agent-aaaaaaaa")
	dispatchAffinityTask(t, &op, 3, "builder-agent-bbbbbbbb")

	before := op.Clone()
	err := op.DispatchTask(fixtures.AffinityDispatch(op.Tasks[1], "builder-agent-bbbbbbbb"))
	if !errors.Is(err, operations.ErrOperationConflict) {
		t.Fatalf("conflicting dispatch = %v, want operation conflict", err)
	}
	if !reflect.DeepEqual(op.Clone(), before) {
		t.Fatal("rejected dispatch changed tasks, events or operation state")
	}
	dispatchAffinityTask(t, &op, 1, "builder-agent-aaaaaaaa")
	assertAffinity(t, op, "builder-agent-aaaaaaaa")

	other := fixtures.AffinityOperation()
	dispatchAffinityTask(t, &other, 0, "builder-agent-bbbbbbbb")
	assertAffinity(t, other, "builder-agent-bbbbbbbb")
	assertAffinity(t, op, "builder-agent-aaaaaaaa")

	unrestricted := fixtures.AffinityOperation()
	unrestricted.Mission.Rules = nil
	dispatchAffinityTask(t, &unrestricted, 0, "builder-agent-aaaaaaaa")
	dispatchAffinityTask(t, &unrestricted, 1, "builder-agent-bbbbbbbb")
	assertAffinity(t, unrestricted, "")
}

func TestOperationAffinitySurvivesSavedOutcomesAndRecovery(t *testing.T) {
	for _, status := range []operations.TaskStatus{
		operations.TaskSucceeded, operations.TaskFailed, operations.TaskInterrupted, operations.TaskCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			op := fixtures.AffinityOperation()
			dispatchAffinityTask(t, &op, 0, "builder-agent-aaaaaaaa")
			// Restore a saved outcome while retaining the original dispatch claim.
			op.Tasks[0].Status = status
			finished := op.Tasks[0].StartedAt.Add(time.Second)
			op.Tasks[0].FinishedAt = &finished
			if status == operations.TaskSucceeded {
				op.Tasks[0].Result = &actions.ActionOutput{ActionName: op.Tasks[0].ActionName, Output: json.RawMessage(`{}`)}
			} else if status != operations.TaskCancelled {
				op.Tasks[0].Error = "execution did not finish successfully"
			}
			assertAffinity(t, op, "builder-agent-aaaaaaaa")
			assertAffinity(t, op.Clone(), "builder-agent-aaaaaaaa")

			data, err := json.Marshal(op)
			if err != nil {
				t.Fatal(err)
			}
			var restored operations.Operation
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			before := restored.Clone()
			if _, err := restored.Recover(finished.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			assertAffinity(t, restored, "builder-agent-aaaaaaaa")
			if !reflect.DeepEqual(restored.Tasks, before.Tasks) {
				t.Fatal("recovery changed saved task assignments or outcomes")
			}
			if err := restored.DispatchTask(fixtures.AffinityDispatch(restored.Tasks[1], "builder-agent-bbbbbbbb")); !errors.Is(err, operations.ErrOperationConflict) {
				t.Fatalf("restored operation accepted another Agent: %v", err)
			}
			dispatchAffinityTask(t, &restored, 1, "builder-agent-aaaaaaaa")
		})
	}

	t.Run("queued cancellation", func(t *testing.T) {
		op := fixtures.AffinityOperation()
		if err := op.Cancel(); err != nil {
			t.Fatal(err)
		}
		assertAffinity(t, op, "")
		if _, err := op.Recover(time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		assertAffinity(t, op, "")
	})
}

func TestOperationAffinityRejectsInvalidHistoryBeforeRecoveryChanges(t *testing.T) {
	for _, name := range []string{"conflicting assignments", "assigned queued task"} {
		t.Run(name, func(t *testing.T) {
			op := fixtures.AffinityOperation()
			if name == "conflicting assignments" {
				dispatchAffinityTask(t, &op, 0, "builder-agent-aaaaaaaa")
				dispatchAffinityTask(t, &op, 1, "builder-agent-aaaaaaaa")
				op.Tasks[1].AgentID = "builder-agent-bbbbbbbb"
			} else {
				dispatch := fixtures.AffinityDispatch(op.Tasks[0], "builder-agent-aaaaaaaa")
				op.Tasks[0].AgentID = dispatch.AgentID
				op.Tasks[0].DeadlineAt = &dispatch.DeadlineAt
			}
			now := time.Now().UTC()
			op.Questions = []operations.Question{{
				ID: id.QuestionID(), Status: operations.QuestionPending,
				Text: "Continue?", AskedAt: now.Add(-2 * time.Minute), FallbackAt: now.Add(-time.Minute),
			}}
			before := op.Clone()
			agentID, err := op.AffinityAgentID()
			if agentID != "" || !errors.Is(err, operations.ErrOperationConflict) {
				t.Fatalf("invalid history gave binding %q, error %v", agentID, err)
			}
			changed, err := op.Recover(now)
			if changed || !errors.Is(err, operations.ErrOperationConflict) {
				t.Fatalf("Recover() = %v, %v; want unchanged operation conflict", changed, err)
			}
			if !reflect.DeepEqual(op.Clone(), before) {
				t.Fatal("invalid recovery changed tasks, changed a question or appended an event")
			}
		})
	}
}

func dispatchAffinityTask(t *testing.T, op *operations.Operation, index int, agentID string) {
	t.Helper()
	if err := op.DispatchTask(fixtures.AffinityDispatch(op.Tasks[index], agentID)); err != nil {
		t.Fatal(err)
	}
}

func assertAffinity(t *testing.T, op operations.Operation, want string) {
	t.Helper()
	agentID, err := op.AffinityAgentID()
	if err != nil || agentID != want {
		t.Fatalf("AffinityAgentID() = %q, %v; want %q", agentID, err, want)
	}
}
