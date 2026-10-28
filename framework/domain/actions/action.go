package actions

import (
	"encoding/json"
	"errors"
	"slices"
)

type Status string

const (
	Unknown     Status = "unknown"
	Ready       Status = "ready"
	Unavailable Status = "unavailable"
)

// Action is the flat inventory snapshot of a definition and its readiness.
type Action struct {
	SessionMode         string        `json:"session_mode,omitempty"`
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
	if a.SessionMode != "" && a.SessionMode != "create" && a.SessionMode != "use" {
		return errors.New("session_mode must be create or use")
	}
	if err := a.definition().Validate(); err != nil {
		return err
	}
	for _, requirement := range a.MissingRequirements {
		if err := requirement.Validate(); err != nil {
			return err
		}
		if requirement.Kind == SecretRequirement && !slices.Contains(a.RequiredSecrets, requirement.Name) {
			return errors.New("missing secret must be declared in required secrets")
		}
	}
	if a.Status == Ready && len(a.MissingRequirements) != 0 {
		return errors.New("ready action cannot have missing requirements")
	}
	return nil
}

// ValidateRequiredSecrets also supports constructors that prepare schemas later.
func (a Action) ValidateRequiredSecrets() error {
	return a.definition().ValidateRequiredSecrets()
}

func (a Action) Definition() Definition {
	return a.definition().Clone()
}

func (a Action) definition() Definition {
	return Definition{
		SessionMode: a.SessionMode,
		Name:        a.Name, Description: a.Description, RequiredSecrets: a.RequiredSecrets,
		InputSchema: a.InputSchema, OutputSchema: a.OutputSchema,
	}
}

func (a Action) Clone() Action {
	definition := a.Definition()
	a.MissingRequirements = slices.Clone(a.MissingRequirements)
	a.RequiredSecrets = definition.RequiredSecrets
	a.InputSchema = definition.InputSchema
	a.OutputSchema = definition.OutputSchema
	return a
}
