package operations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"commander/adapters/api/endpoints"
	"commander/framework/comms"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain"
	"commander/framework/domain/executions"
	"commander/framework/domain/operations"
)

type assignTaskRequest struct {
	StepID     string          `json:"step_id,omitempty"`
	ActionName string          `json:"action_name"`
	Input      json.RawMessage `json:"input,omitempty"`
}

func registerCommands(mux *http.ServeMux, commands controllercommands.Operations) {
	mux.HandleFunc("POST /api/v1/operations", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var command controllercommands.StartOperationCommand
		if !endpoints.ReadJSON(w, r, &command, false) {
			return
		}
		if !json.Valid(command.Input) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := commands.Start(r.Context(), command)
		writeCommandResult(w, http.StatusCreated, "/api/v1/operations/"+operation.ID, operation, err)
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/tasks", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request assignTaskRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := controllercommands.AssignTaskCommand{
			OperationID: r.PathValue("operation_id"), StepID: request.StepID,
			ActionName: request.ActionName, Input: request.Input,
		}
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		task, err := commands.Assign(r.Context(), command)
		location := "/api/v1/operations/" + command.OperationID + "/tasks/" + task.ID
		writeCommandResult(w, http.StatusCreated, location, task, err)
	}))
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/cancel", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		if !endpoints.ReadJSON(w, r, &struct{}{}, true) {
			return
		}
		operation, err := commands.Cancel(r.Context(), controllercommands.CancelOperationCommand{OperationID: r.PathValue("operation_id")})
		writeCommandResult(w, http.StatusOK, "", operation, err)
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
	case errors.Is(err, operations.ErrQuestionPending):
		endpoints.WriteError(w, http.StatusConflict, "question_pending")
	case errors.Is(err, domain.ErrInvalidInput):
		endpoints.WriteError(w, http.StatusUnprocessableEntity, "invalid_input")
	default:
		endpoints.WriteQueryResult(w, nil, err)
	}
}
