package operations

import (
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
)

func registerContinuations(mux *http.ServeMux, commands controllercommands.Operations) {
	mux.HandleFunc("POST /api/v1/operations/{operation_id}/tasks/{task_id}/continuation/resolve", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		var request models.ResolveContinuationRequest
		if !endpoints.ReadJSON(w, r, &request, false) {
			return
		}
		command := mappings.ToResolveContinuationCommand(r.PathValue("operation_id"), r.PathValue("task_id"), request)
		if err := command.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		operation, err := commands.Resolve(r.Context(), command)
		writeCommandResult(w, http.StatusOK, "", mappings.ToOperationResponse(operation), err)
	}))
}
