package endpoints

import (
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
)

func RegisterReports(mux *http.ServeMux, store reports.ReportStore) {
	mux.HandleFunc("GET /api/v1/reports/{report_id}", RequestHandler(func(w http.ResponseWriter, r *http.Request) {
		reportID := r.PathValue("report_id")
		if !id.Valid(reportID) {
			WriteError(w, http.StatusBadRequest, "invalid_report_id")
			return
		}
		report, err := store.Get(r.Context(), reportID)
		WriteQueryResult(w, mappings.ToReportResponse(report), err)
	}))
}
