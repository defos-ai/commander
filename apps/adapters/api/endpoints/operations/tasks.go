package operations

import (
	"net/http"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func registerTasks(mux *http.ServeMux, store operations.OperationStore, agentStore agents.AgentStore) {
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
		responses := mappings.ToTaskResponses(items)
		if err == nil && len(items) != 0 {
			var records map[string]agents.Agent
			records, err = readOperationTaskAgents(r.Context(), store, agentStore, r.PathValue("operation_id"))
			now := time.Now().UTC()
			for i, item := range items {
				if item.OperationID == r.PathValue("operation_id") {
					responses[i] = mappings.ToTaskResponseWithRequirements(item, records[item.AgentID], now)
				}
			}
		}
		endpoints.WriteQueryResult(w, pageResponse(responses, cursor), err)
	}))
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/tasks/{task_id}", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		item, err := store.GetTask(r.Context(), r.PathValue("operation_id"), r.PathValue("task_id"))
		var records map[string]agents.Agent
		if err == nil && item.OperationID == r.PathValue("operation_id") {
			records, err = readOperationTaskAgents(r.Context(), store, agentStore, item.OperationID)
		}
		endpoints.WriteQueryResult(w, mappings.ToTaskResponseWithRequirements(item, records[item.AgentID], time.Now().UTC()), err)
	}))
}
