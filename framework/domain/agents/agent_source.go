package agents

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/defos-ai/commander/framework/agent/packages"
)

type AgentSource struct {
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	Source         string              `json:"source"`
	Visibility     packages.Visibility `json:"visibility"`
	Trusted        bool                `json:"trusted"`
	TrustUpdatedBy string              `json:"trust_updated_by,omitempty"`
	TrustUpdatedAt time.Time           `json:"trust_updated_at,omitzero"`
	Version        uint64              `json:"version"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

func (entry AgentSource) Validate() error {
	if err := ValidateAgentSource(entry.Source); err != nil {
		return err
	}
	source := packages.Sources{SchemaVersion: 1, Agents: map[string]packages.Entry{
		entry.Name: {Description: entry.Description, Source: entry.Source, Visibility: entry.Visibility},
	}}
	if err := source.Validate(); err != nil {
		return err
	}
	if entry.Version == 0 {
		return errors.New("agent source version must be positive")
	}
	if !validAgentSourceTime(entry.CreatedAt) || !validAgentSourceTime(entry.UpdatedAt) || entry.UpdatedAt.Before(entry.CreatedAt) {
		return errors.New("agent source timestamps must be valid and update must not precede creation")
	}
	return entry.validateTrust()
}

func (entry AgentSource) validateTrust() error {
	if entry.Trusted {
		if _, err := packages.ParseReference(entry.Source); err != nil {
			return errors.New("trusted agent sources require an exact release selector")
		}
	}
	if !entry.Trusted && entry.TrustUpdatedBy == "" && entry.TrustUpdatedAt.IsZero() {
		return nil
	}
	if strings.TrimSpace(entry.TrustUpdatedBy) == "" || strings.ContainsFunc(entry.TrustUpdatedBy, unicode.IsControl) {
		return errors.New("agent source trust decisions require a nonblank actor without control characters")
	}
	if !validAgentSourceTime(entry.TrustUpdatedAt) || entry.TrustUpdatedAt.Before(entry.CreatedAt) || entry.TrustUpdatedAt.After(entry.UpdatedAt) {
		return errors.New("agent source trust decision time must be valid and within creation and update times")
	}
	return nil
}

func ValidateAgentSource(value string) error {
	reference, err := packages.ParseSelector(value)
	if err != nil || reference.String() != value {
		return errors.New("agent source must be a canonical full GitHub selector")
	}
	return nil
}

func validAgentSourceTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}
