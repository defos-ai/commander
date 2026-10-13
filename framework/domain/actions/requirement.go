package actions

import (
	"errors"
	"strings"
)

type RequirementKind string

const (
	ExecutableRequirement    RequirementKind = "executable"
	ConfigurationRequirement RequirementKind = "configuration"
)

// Requirements describe missing prerequisites, never configuration values or secrets.
type Requirement struct {
	Kind   RequirementKind `json:"kind"`
	Name   string          `json:"name"`
	Secret bool            `json:"secret,omitempty"`
}

func (r Requirement) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("requirement name must not be blank")
	}
	switch r.Kind {
	case ExecutableRequirement, ConfigurationRequirement:
	default:
		return errors.New("invalid requirement kind")
	}
	if r.Secret && r.Kind != ConfigurationRequirement {
		return errors.New("only configuration requirements can be secret")
	}
	return nil
}
