package operations

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func registerOperations(mux *http.ServeMux, store operations.OperationStore) {
	mux.HandleFunc("GET /api/v1/operations", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		slices.SortFunc(items, func(a, b operations.Operation) int {
			if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
				return order
			}
			return cmp.Compare(a.ID, b.ID)
		})
		endpoints.WriteQueryResult(w, endpoints.ListResponse(mappings.ToOperationResponses(items)), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("operation_id"))
		endpoints.WriteQueryResult(w, mappings.ToOperationResponse(item), err)
	}))
}
