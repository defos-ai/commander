package operations

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/queries"
)

func registerOperations(mux *http.ServeMux, query queries.Operations) {
	mux.HandleFunc("GET /api/v1/operations", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		items, err := query.List(r.Context(), true)
		slices.SortFunc(items, func(a, b queries.OperationView) int {
			if order := b.CreatedAt.Compare(a.CreatedAt); order != 0 {
				return order
			}
			return cmp.Compare(a.ID, b.ID)
		})
		endpoints.WriteQueryResult(w, endpoints.ListResponse(mappings.ToOperationViewResponses(items)), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := query.Get(r.Context(), r.PathValue("operation_id"))
		endpoints.WriteQueryResult(w, mappings.ToOperationViewResponse(item), err)
	}))
}
