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
	SessionMode         string          `json:"session_mode,omitempty"`
	Name                string          `json:"name"`
	Description         string          `json:"description"`
	StatusReason        string          `json:"status_reason,omitempty"`
	Status              Status          `json:"status"`
	MissingRequirements []Requirement   `json:"missing_requirements,omitempty"`
	InputSchema         json.RawMessage `json:"input_schema"`
	OutputSchema        json.RawMessage `json:"output_schema"`
}

// Validate checks shared metadata before it enters agent inventory or the catalog.
// Readiness is interpreted separately for each agent and for catalog entries.
func (a Action) Validate() error {
	if a.SessionMode != "" && a.SessionMode != "create" && a.SessionMode != "use" {
		return errors.New("session_mode must be create or use")
	}
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("action requires a name")
	}
	for _, requirement := range a.MissingRequirements {
		if err := requirement.Validate(); err != nil {
			return err
		}
	}
	if a.Status == Ready && len(a.MissingRequirements) != 0 {
		return errors.New("ready action cannot have missing requirements")
	}
	for name, schema := range map[string]json.RawMessage{"input": a.InputSchema, "output": a.OutputSchema} {
		var value map[string]json.RawMessage
		if err := json.Unmarshal(schema, &value); err != nil || value == nil {
			return fmt.Errorf("action %s schema must be a JSON object", name)
		}
	}
	return nil
}
