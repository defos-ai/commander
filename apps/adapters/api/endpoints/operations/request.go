package operations

import (
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/framework/id"
)

func requestHandler(query http.HandlerFunc) http.HandlerFunc {
	return endpoints.RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"operation_id", "task_id", "question_id"} {
			if value := r.PathValue(name); value != "" && !id.Valid(value) {
				endpoints.WriteError(w, http.StatusBadRequest, "invalid_"+name)
				return
			}
		}
		query(w, r)
	})
}
