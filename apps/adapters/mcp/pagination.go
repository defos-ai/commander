package missionsmcp

import (
	"fmt"

	"github.com/defos-ai/commander/framework/domain"
)

const defaultPageLimit = 100
const maxPageLimit = domain.MaxPageSize

type OperationPageRequest struct {
	OperationRequest
	Cursor string `json:"cursor,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

func (r OperationPageRequest) Validate() error {
	if r.Limit != nil && (*r.Limit < 1 || *r.Limit > maxPageLimit) {
		return fmt.Errorf("limit must be between 1 and %d", maxPageLimit)
	}
	return nil
}

func (r OperationPageRequest) pageLimit() int {
	if r.Limit != nil {
		return *r.Limit
	}
	return defaultPageLimit
}

func operationPageSchema(withWait bool) string {
	properties := map[string]any{
		"operation_id": stringProperty,
		"cursor": map[string]any{
			"type": "string", "description": "Copy next_cursor from the same operation and tool; omit to start.",
		},
		"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": maxPageLimit, "default": defaultPageLimit},
	}
	if withWait {
		properties["wait_seconds"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 30}
	}
	return objectSchema(properties, "operation_id")
}
