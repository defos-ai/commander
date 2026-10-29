package host

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fsoperations "github.com/defos-ai/commander/providers/fs/operations"
)

func QuestionOperations(t *testing.T) operations.OperationStore {
	t.Helper()
	store, err := fsoperations.NewOperationFsStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func PendingQuestion(fallbackAt time.Time) operations.Operation {
	now := time.Now().UTC()
	askedAt := now
	if !askedAt.Before(fallbackAt) {
		askedAt = fallbackAt.Add(-time.Minute)
	}
	return operations.Operation{
		ID: id.OperationID(), Status: operations.Waiting, Mission: missions.Mission{Name: "question-fallback"},
		CreatedAt: now, UpdatedAt: now,
		Questions: []operations.Question{{ID: id.QuestionID(), Status: operations.QuestionPending,
			Text: "Continue this operation?", AskedAt: askedAt, FallbackAt: fallbackAt}},
	}
}
