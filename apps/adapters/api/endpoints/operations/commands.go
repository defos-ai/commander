package operations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"commander/adapters/api/endpoints"
	"commander/adapters/api/mappings"
	"commander/adapters/api/models"
	"commander/framework/comms"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain"
	"commander/framework/domain/executions"
	"commander/framework/domain/operations"
)

func registerCommands(mux *http.ServeMux, commands controllercommands.Operations) {
	mux.HandleFunc("POST /api/v1/operations", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.StartOperationRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := mappings.ToStartOperationCommand(request)
		if command.Validate() != nil || !json.Valid(command.Input) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := commands.Start(r.Context(), command)
		writeCommandResult(w, http.StatusCreated, "/api/v1/operations/"+operation.ID, mappings.ToOperationResponse(operation), err)
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/tasks", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.AssignTaskRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := mappings.ToAssignTaskCommand(r.PathValue("operation_id"), request)
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		task, err := commands.Assign(r.Context(), command)
		location := "/api/v1/operations/" + command.OperationID + "/tasks/" + task.ID
		writeCommandResult(w, http.StatusCreated, location, mappings.ToTaskResponse(task), err)
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/cancel", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.CancelOperationRequest
		if !endpoints.ReadJSON(w, r, &request, true) {
			return
		}
		command := mappings.ToCancelOperationCommand(r.PathValue("operation_id"))
		operation, err := commands.Cancel(r.Context(), command)
		writeCommandResult(w, http.StatusOK, "", mappings.ToOperationResponse(operation), err)
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/complete", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.CompleteOperationRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := mappings.ToCompleteOperationCommand(r.PathValue("operation_id"), request)
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := commands.Complete(r.Context(), command)
		writeCommandResult(w, http.StatusOK, "", mappings.ToOperationResponse(operation), err)
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/fail", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.FailOperationRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := mappings.ToFailOperationCommand(r.PathValue("operation_id"), request)
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := commands.Fail(r.Context(), command)
		writeCommandResult(w, http.StatusOK, "", mappings.ToOperationResponse(operation), err)
	}))
}

func writeCommandResult(w http.ResponseWriter, status int, location string, value any, err error) {
	if err == nil {
		if location != "" {
			w.Header().Set("Location", location)
		}
		endpoints.WriteJSON(w, status, value)
		return
	}
	switch {
	case errors.Is(err, executions.ErrIndeterminate):
		endpoints.WriteError(w, http.StatusServiceUnavailable, "command_outcome_unknown")
	case errors.Is(err, operations.ErrWriteUncertain):
		endpoints.WriteError(w, http.StatusServiceUnavailable, "operation_write_uncertain")
	case errors.Is(err, context.DeadlineExceeded):
		endpoints.WriteError(w, http.StatusGatewayTimeout, "request_timeout")
	case errors.Is(err, context.Canceled):
		endpoints.WriteError(w, http.StatusServiceUnavailable, "request_canceled")
	case errors.Is(err, domain.ErrControllerClosed), errors.Is(err, operations.ErrClosed), errors.Is(err, comms.ErrClosed):
		endpoints.WriteError(w, http.StatusServiceUnavailable, "controller_unavailable")
	case errors.Is(err, operations.ErrOperationConflict):
		endpoints.WriteError(w, http.StatusConflict, "operation_conflict")
	case errors.Is(err, operations.ErrFinished):
		endpoints.WriteError(w, http.StatusConflict, "operation_finished")
	case errors.Is(err, operations.ErrActiveTasks):
		endpoints.WriteError(w, http.StatusConflict, "operation_active_tasks")
	case errors.Is(err, operations.ErrUnresolvedWork):
		endpoints.WriteError(w, http.StatusConflict, "operation_unresolved_work")
	case errors.Is(err, operations.ErrQuestionPending):
		endpoints.WriteError(w, http.StatusConflict, "question_pending")
	case errors.Is(err, operations.ErrQuestionAnswered):
		endpoints.WriteError(w, http.StatusConflict, "question_answered")
	case errors.Is(err, operations.ErrQuestionExpired):
		endpoints.WriteError(w, http.StatusConflict, "question_expired")
	case errors.Is(err, operations.ErrContinuationConflict):
		endpoints.WriteError(w, http.StatusConflict, "continuation_conflict")
	case errors.Is(err, operations.ErrTaskLimitExceeded):
		endpoints.WriteError(w, http.StatusConflict, "task_limit_exceeded")
	case errors.Is(err, operations.ErrUnknownQuestion):
		endpoints.WriteError(w, http.StatusNotFound, "question_not_found")
	case errors.Is(err, domain.ErrInvalidInput):
		endpoints.WriteError(w, http.StatusUnprocessableEntity, "invalid_input")
	case errors.Is(err, domain.ErrInvalidOutput):
		endpoints.WriteError(w, http.StatusUnprocessableEntity, "invalid_output")
	default:
		endpoints.WriteQueryResult(w, nil, err)
	}
}
