package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func CreateQueueOperation(t *testing.T, store *operationfs.OperationFsStore) (operations.Operation, operations.Task) {
	t.Helper()
	created := time.Date(2026, time.September, 29, 10, 0, 0, 0, time.UTC)
	operationID := id.OperationID()
	op := operations.Operation{
		ID: operationID, Status: operations.Planning, CreatedAt: created, UpdatedAt: created,
		Mission:   missions.Mission{Name: "review"},
		Preflight: &operations.Preflight{Ready: true, Version: 2},
		Events: []operations.Event{{Sequence: 1, OperationID: operationID,
			Type: operations.OperationStarted, Status: operations.Planning, CreatedAt: created}},
	}
	if err := store.Create(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(t.Context(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved, operations.NewTask(op.ID, "", actions.NextAction{ActionName: "inspect"})
}
