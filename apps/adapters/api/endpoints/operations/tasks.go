package operations

import (
	"net/http"

	"commander/adapters/api/endpoints"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

func registerTasks(mux *http.ServeMux, store operations.OperationStore) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/tasks", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		cursor, limit, ok := readPage(w, r)
		if !ok {
			return
		}
		if cursor != "" && !id.Valid(cursor) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		items, err := store.ListTasks(r.Context(), r.PathValue("operation_id"), cursor, limit)
		if len(items) > 0 {
			cursor = items[len(items)-1].ID
		}
		endpoints.WriteQueryResult(w, pageResponse(items, cursor), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/tasks/{task_id}", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.GetTask(r.Context(), r.PathValue("operation_id"), r.PathValue("task_id"))
		endpoints.WriteQueryResult(w, item, err)
	}))
}
