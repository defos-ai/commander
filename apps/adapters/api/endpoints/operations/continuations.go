package operations

import (
	"net/http"

	"commander/adapters/api/endpoints"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/domain/actions"
)

type resolveContinuationRequest struct {
	Version uint64               `json:"version"`
	Actions []actions.NextAction `json:"actions"`
}

func registerContinuations(mux *http.ServeMux, commands controllercommands.Operations) {
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/tasks/{task_id}/continuation/resolve", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request resolveContinuationRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := controllercommands.ResolveContinuationCommand{
			OperationID: r.PathValue("operation_id"), TaskID: r.PathValue("task_id"),
			Version: request.Version, Actions: request.Actions,
		}
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := commands.Resolve(r.Context(), command)
		writeCommandResult(w, http.StatusOK, "", operation, err)
	}))
}
