package operation

import (
	"cmp"
	"net/http"
	"slices"

	"commander/adapters/api/endpoints"
	"commander/framework/domain/operations"
)

func registerOperations(mux *http.ServeMux, store operations.OperationStore) {
	mux.HandleFunc("GET /api/v1/operations", queryHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := store.List(r.Context())
		slices.SortFunc(items, func(a, b operations.Operation) int {
			if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
				return order
			}
			return cmp.Compare(a.ID, b.ID)
		})
		endpoints.WriteQueryResult(w, endpoints.ListResponse(items), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}", queryHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.Get(r.Context(), r.PathValue("operation_id"))
		endpoints.WriteQueryResult(w, item, err)
	}))
}
