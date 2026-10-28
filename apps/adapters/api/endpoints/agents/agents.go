package agents

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func Register(mux *http.ServeMux, store agents.AgentStore, catalog agents.CatalogStore) {
	registerCatalog(mux, catalog)
	mux.HandleFunc("GET /api/v1/agents", endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		now := time.Now().UTC()
		for i := range items {
			items[i] = items[i].Snapshot(now)
		}
		slices.SortFunc(items, func(a, b agents.Agent) int { return cmp.Compare(a.ID, b.ID) })
		endpoints.WriteQueryResult(w, endpoints.ListResponse(mappings.ToAgentResponses(items)), err)
	}))
	mux.HandleFunc("GET /api/v1/agents/{agent_id}", endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("agent_id"))
		endpoints.WriteQueryResult(w, mappings.ToAgentResponse(item.Snapshot(time.Now().UTC())), err)
	}))
}
