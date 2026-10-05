package mappings

import (
	"bytes"

	"commander/adapters/api/models"
	"commander/framework/domain/reports"
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
