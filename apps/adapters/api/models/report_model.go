package models

import (
	"encoding/json"
	"time"
)

type ReportResponse struct {
	ID          string          `json:"id"`
	OperationID string          `json:"operation_id"`
	Mission     string          `json:"mission"`
	Output      json.RawMessage `json:"output"`
	CreatedAt   time.Time       `json:"created_at"`
}
