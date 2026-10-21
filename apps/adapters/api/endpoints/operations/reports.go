package operations

import (
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/queries"
)

func registerReports(mux *http.ServeMux, query queries.Operations) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/report", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		report, err := query.GetReport(r.Context(), r.PathValue("operation_id"))
		endpoints.WriteQueryResult(w, mappings.ToReportResponse(report), err)
	}))
}
