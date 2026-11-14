package agents

import (
	"errors"
	"strings"
	"unicode/utf8"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

// Dependency describes external software; Version is guidance, not an installed-version check.
type Dependency struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Version     string `json:"version,omitempty" yaml:"version,omitempty"`
}

func ValidateDependencies(dependencies []Dependency) error {
	if len(dependencies) > 100 {
		return errors.New("agent dependencies must contain at most 100 entries")
	}
	seen := make(map[string]bool, len(dependencies))
	for _, dependency := range dependencies {
		if !validDependencyText(dependency.Name, 128) {
			return errors.New("dependency name must be trimmed, nonblank UTF-8 text of at most 128 bytes")
		}
		if !validDependencyText(dependency.Description, 8192) {
			return errors.New("dependency description must be trimmed, nonblank UTF-8 text of at most 8192 bytes")
		}
		if dependency.Version != "" && !validDependencyText(dependency.Version, 256) {
			return errors.New("dependency version must be trimmed UTF-8 text of at most 256 bytes when supplied")
		}
		if seen[dependency.Name] {
			return errors.New("dependency names must be unique within an agent")
		}
		seen[dependency.Name] = true
	}
	return nil
}

func (snapshot AgentSnapshot) DependencyFor(requirement actionmodel.Requirement) (Dependency, bool) {
	if requirement.Kind == actionmodel.ExecutableRequirement {
		for _, dependency := range snapshot.Dependencies {
			if dependency.Name == requirement.Name {
				return dependency, true
			}
		}
	}
	return Dependency{}, false
}

func validDependencyText(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && strings.TrimSpace(value) == value && utf8.ValidString(value)
}
