package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

type Status string

const (
	Unknown     Status = "unknown"
	Ready       Status = "ready"
	Unavailable Status = "unavailable"
)

type Action struct {
	Name                string        `json:"name"`
	Description         string        `json:"description"`
	StatusReason        string        `json:"status_reason,omitempty"`
	Status              Status        `json:"status"`
	MissingRequirements []Requirement `json:"missing_requirements,omitempty"`
	// RequiredSecrets remains declared even when discovery succeeds. Values never belong here.
	RequiredSecrets []string        `json:"required_secrets,omitempty"`
	InputSchema     json.RawMessage `json:"input_schema"`
	OutputSchema    json.RawMessage `json:"output_schema"`
}

// Validate checks shared metadata before it enters agent inventory or the catalog.
// Readiness is interpreted separately for each agent and for catalog entries.
func (a Action) Validate() error {
	if strings.TrimSpace(a.Name) == "" {
		return errors.New("action requires a name")
	}
	if err := a.ValidateRequiredSecrets(); err != nil {
		return err
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

// ValidateRequiredSecrets also supports constructors that prepare schemas later.
func (a Action) ValidateRequiredSecrets() error {
	seen := make(map[string]bool, len(a.RequiredSecrets))
	for _, name := range a.RequiredSecrets {
		if err := secrets.ValidateName(name); err != nil {
			return fmt.Errorf("required secret: %w", err)
		}
		if seen[name] {
			return errors.New("required secret names must be unique within an action")
		}
		seen[name] = true
	}
	return nil
}

func (a Action) Clone() Action {
	a.MissingRequirements = slices.Clone(a.MissingRequirements)
	a.RequiredSecrets = slices.Clone(a.RequiredSecrets)
	a.InputSchema = bytes.Clone(a.InputSchema)
	a.OutputSchema = bytes.Clone(a.OutputSchema)
	return a
}
