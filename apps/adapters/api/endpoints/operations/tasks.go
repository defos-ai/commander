package operations

import (
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/queries"
)

func registerTasks(mux *http.ServeMux, query queries.Operations) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/tasks", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		cursor, limit, ok := readPage(w, r)
		if !ok {
			return
		}
		if cursor != "" && !id.Valid(cursor) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		items, err := query.ListTasks(r.Context(), r.PathValue("operation_id"), cursor, limit)
		if len(items) > 0 {
			cursor = items[len(items)-1].ID
		}
		endpoints.WriteQueryResult(w, pageResponse(mappings.ToTaskViewResponses(items), cursor), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/tasks/{task_id}", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := query.GetTask(r.Context(), r.PathValue("operation_id"), r.PathValue("task_id"))
		endpoints.WriteQueryResult(w, mappings.ToTaskViewResponse(item), err)
	}))
}
