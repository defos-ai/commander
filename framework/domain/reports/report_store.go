package reports

import (
	"context"
)

// ReportStore publishes immutable reports. Repeating Put with the same ID and
// content succeeds; different content for an existing ID returns
// ErrReportConflict. Implementations retain and return independent copies.
type ReportStore interface {
	Put(context.Context, Report) error
	Get(context.Context, string) (Report, error)
}
