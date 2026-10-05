package operations

import (
	"net/http"
	"time"

	"commander/adapters/api/endpoints"
	"commander/framework/domain/operations"
)

type questionList struct {
	Items              []operations.Question `json:"items"`
	LastQuestion       *operations.Question  `json:"last_question"`
	RemainingQuestions int                   `json:"remaining_questions"`
}

func registerQuestions(mux *http.ServeMux, store operations.OperationStore) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/questions", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		operation, err := store.Get(r.Context(), r.PathValue("operation_id"))
		endpoints.WriteQueryResult(w, questionsResponse(operation, time.Now().UTC()), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/questions/{question_id}", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		operation, err := store.Get(r.Context(), r.PathValue("operation_id"))
		if err != nil {
			endpoints.WriteQueryResult(w, nil, err)
			return
		}
		for _, question := range operation.Questions {
			if question.ID == r.PathValue("question_id") {
				endpoints.WriteQueryResult(w, question, nil)
				return
			}
		}
		endpoints.WriteError(w, http.StatusNotFound, "question_not_found")
	}))
}

func questionsResponse(operation operations.Operation, now time.Time) questionList {
	result := questionList{Items: endpoints.ListResponse(operation.Questions).Items}
	if operation.Status.Terminal() {
		return result
	}
	count := 0
	for i := range result.Items {
		question := &result.Items[i]
		if !question.Pending() || !question.ExpiresAt.IsZero() && !now.Before(question.ExpiresAt) {
			continue
		}
		count++
		latest := result.LastQuestion
		if latest == nil || question.AskedAt.After(latest.AskedAt) || question.AskedAt.Equal(latest.AskedAt) && question.ID > latest.ID {
			result.LastQuestion = question
		}
	}
	result.RemainingQuestions = max(0, count-1)
	return result
}
