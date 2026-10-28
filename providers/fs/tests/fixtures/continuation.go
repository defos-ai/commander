package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func CreateContinuationOperation(t *testing.T, store *operationfs.OperationFsStore) (operations.Operation, []actions.NextAction) {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	operationID, taskID := id.OperationID(), id.TaskID()
	op := operations.Operation{
		ID: operationID, Status: operations.Waiting, CreatedAt: created, UpdatedAt: created,
		Tasks: []operations.Task{{ID: taskID, OperationID: operationID, CommandID: taskID,
			ParentTaskID: id.TaskID(), StepID: "review", ActionName: "discover", Status: operations.TaskSucceeded,
			Continuation: &operations.Continuation{Version: 7, DecisionBy: operations.DecisionModel,
				Status: operations.ContinuationPending, CreatedTaskIDs: []string{},
				ProposedActions: []actions.NextAction{{ActionName: "inspect", Input: json.RawMessage(`{"page":0}`)}},
			},
		}},
		Events: []operations.Event{{Sequence: 1, OperationID: operationID, TaskID: taskID,
			Type: operations.ContinuationReady, Status: operations.Waiting, CreatedAt: created}},
	}
	selected := []actions.NextAction{
		{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`), Reason: "Inspect first page"},
		{ActionName: "report", Input: json.RawMessage(`{"page":2}`), Reason: "Report second page"},
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	return op, selected
}
