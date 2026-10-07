package endpoints

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func RegisterAgents(mux *http.ServeMux, store agents.AgentStore) {
	mux.HandleFunc("GET /api/v1/agents", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		now := time.Now().UTC()
		for i := range items {
			items[i] = items[i].Snapshot(now)
		}
		slices.SortFunc(items, func(a, b agents.Agent) int { return cmp.Compare(a.ID, b.ID) })
		WriteQueryResult(w, ListResponse(mappings.ToAgentResponses(items)), err)
	}))
	mux.HandleFunc("GET /api/v1/agents/{agent_id}", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("agent_id"))
		WriteQueryResult(w, mappings.ToAgentResponse(item.Snapshot(time.Now().UTC())), err)
	}))
}
