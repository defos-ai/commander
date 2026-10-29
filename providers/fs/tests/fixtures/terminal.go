package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	operationfs "github.com/defos-ai/commander/providers/fs/operations"
)

func CompletionProposal() operations.CompletionProposal {
	return operations.CompletionProposal{ID: id.New(),
		Output:     json.RawMessage(`{ "summary":"Review complete", "identifier":9007199254740993, "path":"\u0061\/b", "markup":"<p>" }`),
		ProposedBy: operations.CompletionModel, ProposedAt: time.Date(2026, time.October, 5, 10, 0, 0, 123456789, time.UTC)}
}

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
