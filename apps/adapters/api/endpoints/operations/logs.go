package operations

import (
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/framework/queries"
)

func registerLogs(mux *http.ServeMux, query queries.Operations) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/logs", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		cursor, limit, ok := readPage(w, r, "task_id", "agent_id", "action_name", "order")
		if !ok {
			return
		}
		if cursor != "" && !id.Valid(cursor) {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_cursor")
			return
		}
		values := r.URL.Query()
		order := executions.LogOrderAscending
		if values.Has("order") {
			order = executions.LogOrder(values.Get("order"))
			if order != executions.LogOrderAscending && order != executions.LogOrderDescending {
				endpoints.WriteError(w, http.StatusBadRequest, "invalid_query")
				return
			}
		}
		filter := executions.LogQuery{
			OperationID: r.PathValue("operation_id"), TaskID: values.Get("task_id"),
			AgentID: values.Get("agent_id"), ActionName: values.Get("action_name"),
			AfterLogID: cursor, Limit: limit, Order: order,
		}
		if (values.Has("task_id") && filter.TaskID == "") || (values.Has("agent_id") && filter.AgentID == "") ||
			(values.Has("action_name") && filter.ActionName == "") || filter.Validate() != nil {
			endpoints.WriteError(w, http.StatusBadRequest, "invalid_query")
			return
		}
		logs, err := query.ListLogs(r.Context(), filter)
		if len(logs) > 0 {
			cursor = logs[len(logs)-1].ID
		}
		endpoints.WriteQueryResult(w, pageResponse(mappings.ToLogResponses(logs), cursor), err)
	}))
}
