package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func CreateResultOperation(t *testing.T, store *operationfs.OperationFsStore) (operations.Operation, operations.TaskResult) {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	operationID, taskID := id.OperationID(), id.TaskID()
	op := operations.Operation{
		ID: operationID, Status: operations.Running, CreatedAt: created, UpdatedAt: created,
		Mission: missions.Mission{Rules: missions.RuleSet{
			missions.AggregateResults:  {ForActions: []string{"discover"}},
			missions.DecideNextActions: {ForActions: []string{"inspect"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
		}},
		Preflight: &operations.Preflight{Ready: true, Version: 2},
		Tasks: []operations.Task{{ID: taskID, OperationID: operationID, CommandID: taskID,
			AgentID: "review-agent-00000001", ActionName: "discover", StepID: "review",
			Status: operations.TaskRunning, StartedAt: created}},
		Events: []operations.Event{{Sequence: 1, OperationID: operationID, TaskID: taskID,
			Type: operations.TaskDispatched, Status: operations.Running, CreatedAt: created}},
	}
	request := operations.TaskResult{TaskID: taskID, AgentID: op.Tasks[0].AgentID,
		Status: operations.TaskSucceeded, FinishedAt: created.Add(time.Minute),
		Result: &actions.ActionOutput{ActionName: "discover", Output: json.RawMessage(`{"found":2}`),
			NextActions: []actions.NextAction{
				{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
				{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)},
			}},
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op, request
}
