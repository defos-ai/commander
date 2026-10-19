package operations

import (
	"cmp"
	"net/http"
	"slices"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func registerOperations(mux *http.ServeMux, store operations.OperationStore, agentStore agents.AgentStore) {
	mux.HandleFunc("GET /api/v1/operations", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		slices.SortFunc(items, func(a, b operations.Operation) int {
			if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
				return order
			}
			return cmp.Compare(a.ID, b.ID)
		})
		responses := mappings.ToOperationResponses(items)
		if err == nil && slices.ContainsFunc(items, func(item operations.Operation) bool { return !item.Status.Terminal() }) {
			var records map[string]agents.Agent
			records, err = readTaskAgents(r.Context(), agentStore)
			now := time.Now().UTC()
			for i, item := range items {
				responses[i] = mappings.ToOperationResponseWithRequirements(item, records, now)
			}
		}
		endpoints.WriteQueryResult(w, endpoints.ListResponse(responses), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("operation_id"))
		var records map[string]agents.Agent
		if err == nil && !item.Status.Terminal() {
			records, err = readTaskAgents(r.Context(), agentStore)
		}
		endpoints.WriteQueryResult(w, mappings.ToOperationResponseWithRequirements(item, records, time.Now().UTC()), err)
	}))
}
