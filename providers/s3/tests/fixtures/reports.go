package fixtures

import (
	"encoding/json"
	"time"

	"github.com/defos-ai/commander/framework/domain/reports"
	"github.com/defos-ai/commander/framework/id"
)

func Report() reports.Report {
	return reports.Report{ID: id.New(), OperationID: id.New(), Mission: "review-website-with-browser", Output: json.RawMessage(`{"summary":"reviewed","serial":9007199254740993}`), CreatedAt: time.Now().UTC()}
}
