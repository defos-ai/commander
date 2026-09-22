package reports

import "errors"

var (
	ErrUnknownReport  = errors.New("unknown report")
	ErrReportConflict = errors.New("report content conflict")
)
