package operations

import (
	"net/http"
	"strconv"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func registerEvents(mux *http.ServeMux, store operations.OperationStore) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/events", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		cursor, limit, ok := readPage(w, r)
		if !ok {
			return
		}
		var sequence uint64
		if cursor != "" {
			var err error
			sequence, err = strconv.ParseUint(cursor, 10, 64)
			if err != nil {
				endpoints.WriteError(w, http.StatusBadRequest, "invalid_cursor")
				return
			}
		}
		items, err := store.ListEvents(r.Context(), r.PathValue("operation_id"), sequence, limit)
		if len(items) > 0 {
			cursor = strconv.FormatUint(items[len(items)-1].Sequence, 10)
		}
		endpoints.WriteQueryResult(w, pageResponse(mappings.ToEventResponses(items), cursor), err)
	}))
}
