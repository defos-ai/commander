package fixtures

import (
	"encoding/json"
	"testing"

	"github.com/defos-ai/commander/framework/domain/operations"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func CreateCompletionOperation(t *testing.T, store *operationfs.OperationFsStore) operations.Operation {
	t.Helper()
	op, _ := CreateContinuationOperation(t, store)
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
