package fixtures

import (
	"time"

	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
)

func QuestionFallbackOperation(fallback time.Time) bridge.Operation {
	now := time.Now().UTC()
	return bridge.Operation{ID: QuestionOperationID, Status: "waiting", UpdatedAt: now,
		Progress: bridge.Progress{TaskCounts: map[string]int{}, PendingQuestions: 1},
		Questions: []bridge.Question{{ID: QuestionID, Text: "Continue with the existing plan?", Status: bridge.QuestionPending,
			AskedAt: now.Add(-time.Minute), FallbackAt: fallback}},
	}
}
