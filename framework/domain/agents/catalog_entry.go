package agents

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/defos-ai/commander/framework/agent/packages"
)

type CatalogEntry struct {
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	Package        string              `json:"package"`
	Visibility     packages.Visibility `json:"visibility"`
	Trusted        bool                `json:"trusted"`
	TrustUpdatedBy string              `json:"trust_updated_by,omitempty"`
	TrustUpdatedAt time.Time           `json:"trust_updated_at,omitzero"`
	Version        uint64              `json:"version"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

func (entry CatalogEntry) Validate() error {
	if err := ValidateCatalogPackage(entry.Package); err != nil {
		return err
	}
	source := packages.Catalog{SchemaVersion: 1, Agents: map[string]packages.Entry{
		entry.Name: {Description: entry.Description, Package: entry.Package, Visibility: entry.Visibility},
	}}
	if err := source.Validate(); err != nil {
		return err
	}
	if entry.Version == 0 {
		return errors.New("catalog entry version must be positive")
	}
	if !validCatalogTime(entry.CreatedAt) || !validCatalogTime(entry.UpdatedAt) || entry.UpdatedAt.Before(entry.CreatedAt) {
		return errors.New("catalog entry timestamps must be valid and update must not precede creation")
	}
	return entry.validateTrust()
}

func (entry CatalogEntry) validateTrust() error {
	if entry.Trusted {
		if _, err := packages.ParseReference(entry.Package); err != nil {
			return errors.New("trusted catalog entries require an exact release package")
		}
	}
	if !entry.Trusted && entry.TrustUpdatedBy == "" && entry.TrustUpdatedAt.IsZero() {
		return nil
	}
	if strings.TrimSpace(entry.TrustUpdatedBy) == "" || strings.ContainsFunc(entry.TrustUpdatedBy, unicode.IsControl) {
		return errors.New("catalog trust decisions require a nonblank actor without control characters")
	}
	if !validCatalogTime(entry.TrustUpdatedAt) || entry.TrustUpdatedAt.Before(entry.CreatedAt) || entry.TrustUpdatedAt.After(entry.UpdatedAt) {
		return errors.New("catalog trust decision time must be valid and within creation and update times")
	}
	return nil
}

func ValidateCatalogPackage(value string) error {
	reference, err := packages.ParseSelector(value)
	if err != nil || reference.String() != value {
		return errors.New("catalog package must be a canonical full GitHub package selector")
	}
	return nil
}

func validCatalogTime(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}
