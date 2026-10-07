package mappings

import (
	"bytes"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/reports"
)

func ToReportResponse(report reports.Report) models.ReportResponse {
	return models.ReportResponse{
		ID:          report.ID,
		OperationID: report.OperationID,
		Mission:     report.Mission,
		Output:      bytes.Clone(report.Output),
		CreatedAt:   report.CreatedAt,
	}
}
