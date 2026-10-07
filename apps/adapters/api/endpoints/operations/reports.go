package operations

import (
	"net/http"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/reports"
)

func registerReports(mux *http.ServeMux, store operations.OperationStore, reportStore reports.ReportStore) {
	mux.HandleFunc("GET /api/v1/operations/{operation_id}/report", requestHandler(func(w http.ResponseWriter, r *http.Request) {
		operation, err := store.Get(r.Context(), r.PathValue("operation_id"))
		if err != nil {
			endpoints.WriteQueryResult(w, nil, err)
			return
		}
		if operation.Status != operations.Completed || operation.ReportID == "" {
			endpoints.WriteQueryResult(w, nil, reports.ErrUnknownReport)
			return
		}
		report, err := reportStore.Get(r.Context(), operation.ReportID)
		if err != nil {
			endpoints.WriteQueryResult(w, nil, err)
			return
		}
		if report.OperationID != operation.ID || report.Mission != operation.Mission.Name {
			endpoints.WriteError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		endpoints.WriteQueryResult(w, mappings.ToReportResponse(report), nil)
	}))
}
