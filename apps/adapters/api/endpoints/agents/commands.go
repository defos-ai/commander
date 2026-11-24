package agents

import (
	"net/http"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	commands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func RegisterCommands(mux *http.ServeMux, controller commands.Controller) {
	mux.HandleFunc("POST /api/v1/agents/stop", endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		var input models.StopAgentsRequest
		if !endpoints.ReadJSON(w, r, &input, false) {
			return
		}
		selector := mappings.ToStopSelector(input.Selector)
		if err := selector.Validate(); err != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if controller == nil {
			endpoints.WriteError(w, http.StatusServiceUnavailable, "agent_controls_unavailable")
			return
		}
		result, err := controller.StopAgents(r.Context(), selector)
		if err != nil || result.Validate() != nil {
			endpoints.WriteError(w, http.StatusServiceUnavailable, "agent_stop_uncertain")
			return
		}
		status := http.StatusOK
		now := time.Now().UTC()
		for i, item := range result.Agents {
			if item.Status == agents.AgentStopRequested {
				status = http.StatusAccepted
			}
			result.Agents[i] = item.Snapshot(now)
		}
		endpoints.WriteJSON(w, status, mappings.ToStopAgentsResponse(result))
	}))
}
