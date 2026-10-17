package actions

import (
	"errors"
	"strings"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

type RequirementKind string

const (
	ExecutableRequirement    RequirementKind = "executable"
	ConfigurationRequirement RequirementKind = "configuration"
	SecretRequirement        RequirementKind = "secret"
)

// Requirements describe missing prerequisites, never configuration or secret values.
type Requirement struct {
	Kind RequirementKind `json:"kind"`
	Name string          `json:"name"`
	// Secret marks sensitive host configuration; managed values use SecretRequirement.
	Secret bool `json:"secret,omitempty"`
}

func (r Requirement) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("requirement name must not be blank")
	}
	switch r.Kind {
	case ExecutableRequirement, ConfigurationRequirement:
	case SecretRequirement:
		if err := secrets.ValidateName(r.Name); err != nil {
			return err
		}
	default:
		return errors.New("invalid requirement kind")
	}
	if r.Secret && r.Kind != ConfigurationRequirement {
		return errors.New("only configuration requirements can use the sensitivity flag")
	}
	return nil
}
