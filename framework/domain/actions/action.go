package actions

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Status string

const (
	Unknown     Status = "unknown"
	Ready       Status = "ready"
	Unavailable Status = "unavailable"
)

type Action struct {
	Name                string          `json:"name"`
	Description         string          `json:"description"`
	StatusReason        string          `json:"status_reason,omitempty"`
	Status              Status          `json:"status"`
	RequiredPermissions []string        `json:"required_permissions"`
	InputSchema         json.RawMessage `json:"input_schema"`
	OutputSchema        json.RawMessage `json:"output_schema"`
}

// Validate checks shared metadata before it enters agent inventory or the catalog.
// Readiness is interpreted separately for each agent and for catalog entries.
func (a Action) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("action requires a name")
	}
	for _, permission := range a.RequiredPermissions {
		if strings.TrimSpace(permission) == "" {
			return errors.New("action permissions must not be blank")
		}
	}
	for name, schema := range map[string]json.RawMessage{"input": a.InputSchema, "output": a.OutputSchema} {
		var value map[string]json.RawMessage
		if err := json.Unmarshal(schema, &value); err != nil || value == nil {
			return fmt.Errorf("action %s schema must be a JSON object", name)
		}
	}
	return nil
}
