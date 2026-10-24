package endpoints

import (
	"context"
	"errors"
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/queries"
)

func RegisterSessions(mux *http.ServeMux, store sessions.Store, agents agents.AgentStore, controller commands.Operations) {
	mux.HandleFunc("GET /api/v1/sessions", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		scope := r.URL.Query().Get("scope_id")
		if !id.Valid(scope) {
			WriteError(w, 400, "invalid_scope_id")
			return
		}
		if store == nil {
			WriteError(w, 503, "sessions_unavailable")
			return
		}
		items, err := (queries.Sessions{Store: store, Agents: agents}).List(r.Context(), scope)
		WriteQueryResult(w, ListResponse(mappings.ToRuntimeSessionResponses(items)), err)
	}))
	mux.HandleFunc("POST /api/v1/sessions/control", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		var input models.ControlSessionRequest
		if !ReadJSON(w, r, &input, false) {
			return
		}
		request := sessions.Control{Reference: sessions.Reference{ID: input.ID, ScopeID: input.ScopeID, OwnerAgentID: input.OwnerAgentID}, Method: input.Method, RequestID: input.RequestID}
		if err := request.Validate(); err != nil {
			WriteError(w, 400, "invalid_request")
			return
		}
		controls, ok := controller.(commands.SessionController)
		if !ok {
			WriteError(w, 503, "sessions_unavailable")
			return
		}
		result, err := controls.ControlSession(r.Context(), request)
		if err != nil {
			switch {
			case errors.Is(err, sessions.ErrWriteUncertain), errors.Is(err, executions.ErrIndeterminate), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
				WriteError(w, 503, "session_control_uncertain")
			default:
				WriteError(w, 422, err.Error())
			}
			return
		}
		WriteJSON(w, 200, mappings.ToRuntimeSessionResponse(result))
	}))
}
