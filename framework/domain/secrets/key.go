// Package secrets describes scoped secret metadata, encrypted values and requests.
// It does not provide encryption or authorize access to secrets.
package secrets

import (
	"errors"

	"github.com/defos-ai/commander/framework/id"
)

// Scope is independent of an operation or an individual agent process.
// AgentType uses the registered type, not a prefix extracted from an agent ID.
type Scope struct {
	AgentType string `json:"agent_type"`
}

func NewScope(agentType string) (Scope, error) {
	scope := Scope{AgentType: agentType}
	return scope, scope.Validate()
}

func (scope Scope) Validate() error {
	if !id.ValidType(scope.AgentType) {
		return errors.New("secret scope requires a valid agent type")
	}
	return nil
}

// Key identifies a named secret within a scope. Names describe configuration,
// never its value. Providers must not treat keys as filesystem paths.
type Key struct {
	Scope Scope  `json:"scope"`
	Name  string `json:"name"`
}

func (key Key) Validate() error {
	if err := key.Scope.Validate(); err != nil {
		return err
	}
	return ValidateName(key.Name)
}

func ValidateName(name string) error {
	if len(name) == 0 || len(name) > 128 {
		return errors.New("secret name must be between 1 and 128 ASCII characters")
	}
	for i, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		case i > 0 && (c == '-' || c == '.'):
		default:
			return errors.New("secret name must start with a letter, digit or underscore and contain only letters, digits, underscores, dots or hyphens")
		}
	}
	return nil
}
