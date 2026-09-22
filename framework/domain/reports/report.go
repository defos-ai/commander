package reports

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"commander/framework/id"
)

type Report struct {
	ID          string          `json:"id"`
	OperationID string          `json:"operation_id"`
	Mission     string          `json:"mission"`
	Output      json.RawMessage `json:"output"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Validate checks the report envelope. Mission output-schema validation belongs
// to the runner; a report may contain any valid JSON value.
func (r Report) Validate() error {
	if !id.Valid(r.ID) {
		return errors.New("report ID must be a canonical UUID")
	}
	if !id.Valid(r.OperationID) {
		return errors.New("report operation ID must be a canonical UUID")
	}
	if strings.TrimSpace(r.Mission) == "" || !utf8.ValidString(r.Mission) {
		return errors.New("report mission must be nonblank UTF-8")
	}
	if !utf8.Valid(r.Output) || !json.Valid(r.Output) {
		return errors.New("report output must be valid JSON")
	}
	if r.CreatedAt.IsZero() {
		return errors.New("report creation time must not be zero")
	}
	return nil
}

func (r Report) Clone() Report {
	r.Output = append(json.RawMessage(nil), r.Output...)
	return r
}
