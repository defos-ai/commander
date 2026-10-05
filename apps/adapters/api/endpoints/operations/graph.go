package operations

import (
	"net/http"

	"commander/adapters/api/endpoints"
	"commander/adapters/api/mappings"
	"commander/framework/domain/operations"
)

func registerGraph(mux *http.ServeMux, store operations.OperationStore) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/graph", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		operation, err := store.Get(r.Context(), r.PathValue("operation_id"))
		endpoints.WriteQueryResult(w, mappings.ToOperationGraphResponse(operation), err)
	}))
}
