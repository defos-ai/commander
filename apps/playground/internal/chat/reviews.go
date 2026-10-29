package chat

import bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"

// Each continuation version or settled operation status is reviewed once.
type reviewKey struct {
	operationID, taskID, questionID, status string
	version                                 uint64
}

func currentReviews(operation bridge.Operation) []reviewKey {
	if operation.Settled() {
		return []reviewKey{{operationID: operation.ID, status: operation.Status}}
	}
	if operation.Terminal() {
		return nil
	}
	var keys []reviewKey
	if p := operation.Preflight; p != nil {
		if !p.Ready || p.Version > 1 && len(operation.Tasks) == 0 {
			keys = append(keys, reviewKey{operationID: operation.ID, status: "preflight", version: p.Version})
		}
	}
	for _, task := range operation.Tasks {
		c := task.Continuation
		if c != nil && c.Status == "pending" && c.DecisionBy == "model" {
			keys = append(keys, reviewKey{operationID: operation.ID, taskID: task.ID, version: c.Version})
		}
	}
	// An operation-wide answer can wake review without a task continuation.
	for _, question := range operation.Questions {
		switch question.Status {
		case bridge.QuestionAnswered:
			keys = append(keys, reviewKey{operationID: operation.ID, questionID: question.ID, status: string(question.Status)})
		}
	}
	return keys
}

func decisions(operation bridge.Operation, seen map[reviewKey]bool) []job {
	var reviews []reviewKey
	for _, key := range currentReviews(operation) {
		if !seen[key] {
			seen[key] = true
			reviews = append(reviews, key)
		}
	}
	if len(reviews) == 0 {
		return nil
	}
	return []job{{operationID: operation.ID, reviews: reviews}}
}

func relevant(operation bridge.Operation, queued []reviewKey) bool {
	for _, current := range currentReviews(operation) {
		for _, key := range queued {
			if current == key {
				return true
			}
		}
	}
	return false
}
