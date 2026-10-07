package operations

import (
	"net/http"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func registerQuestions(mux *http.ServeMux, store operations.OperationStore, commands controllercommands.Operations) {
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
				endpoints.WriteQueryResult(w, mappings.ToQuestionResponse(question), nil)
				return
			}
		}
		endpoints.WriteError(w, http.StatusNotFound, "question_not_found")
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/questions", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.AskQuestionRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := mappings.ToAskQuestionCommand(r.PathValue("operation_id"), request)
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		question, err := commands.Ask(r.Context(), command)
		location := "/api/v1/operations/" + command.OperationID + "/questions/" + question.ID
		writeCommandResult(w, http.StatusCreated, location, mappings.ToQuestionResponse(question), err)
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/questions/{question_id}/answer", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.AnswerQuestionRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := mappings.ToAnswerQuestionCommand(r.PathValue("operation_id"), r.PathValue("question_id"), request)
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := commands.Answer(r.Context(), command)
		writeCommandResult(w, http.StatusOK, "", mappings.ToOperationResponse(operation), err)
	}))
}

func questionsResponse(operation operations.Operation, now time.Time) models.QuestionListResponse {
	result := models.QuestionListResponse{Items: endpoints.ListResponse(mappings.ToQuestionResponses(operation.Questions)).Items}
	if operation.Status.Terminal() {
		return result
	}
	count := 0
	for i, question := range operation.Questions {
		if !question.Pending() || !question.ExpiresAt.IsZero() && !now.Before(question.ExpiresAt) {
			continue
		}
		count++
		latest := result.LastQuestion
		if latest == nil || question.AskedAt.After(latest.AskedAt) || question.AskedAt.Equal(latest.AskedAt) && question.ID > latest.ID {
			result.LastQuestion = &result.Items[i]
		}
	}
	result.RemainingQuestions = max(0, count-1)
	return result
}
