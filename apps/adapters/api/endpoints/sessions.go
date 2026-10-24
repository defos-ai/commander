package endpoints

import (
	"context"
	"errors"
	"net/http"
	"net/url"

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
	query := queries.Sessions{Store: store, Agents: agents}
	mux.HandleFunc("GET /api/v1/sessions", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		filter, ok := readSessionQuery(w, r)
		if !ok {
			return
		}
		if store == nil {
			WriteError(w, 503, "sessions_unavailable")
			return
		}
		items, err := query.List(r.Context(), filter)
		WriteQueryResult(w, ListResponse(mappings.ToRuntimeSessionResponses(items)), err)
	}))
	mux.HandleFunc("GET /api/v1/sessions/{session_id}", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		if !id.Valid(r.PathValue("session_id")) {
			WriteError(w, http.StatusBadRequest, "invalid_session_id")
			return
		}
		if r.URL.RawQuery != "" {
			WriteError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		if store == nil {
			WriteError(w, http.StatusServiceUnavailable, "sessions_unavailable")
			return
		}
		item, err := query.Get(r.Context(), r.PathValue("session_id"))
		WriteQueryResult(w, mappings.ToRuntimeSessionResponse(item), err)
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

func readSessionQuery(w http.ResponseWriter, r *http.Request) (sessions.Query, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_query")
		return sessions.Query{}, false
	}
	for name, value := range values {
		if (name != "scope_id" && name != "owner_agent_id") || len(value) != 1 || value[0] == "" {
			WriteError(w, http.StatusBadRequest, "invalid_query")
			return sessions.Query{}, false
		}
	}
	filter := sessions.Query{ScopeID: values.Get("scope_id"), OwnerAgentID: values.Get("owner_agent_id")}
	if filter.ScopeID != "" && !id.Valid(filter.ScopeID) {
		WriteError(w, http.StatusBadRequest, "invalid_scope_id")
		return sessions.Query{}, false
	}
	if filter.OwnerAgentID != "" && !id.ValidAgent(filter.OwnerAgentID) {
		WriteError(w, http.StatusBadRequest, "invalid_owner_agent_id")
		return sessions.Query{}, false
	}
	return filter, true
}
