// Package secrets describes shared secret metadata, encrypted values and requests.
// It does not provide encryption or authorize access to secrets.
package secrets

import "errors"

// Key identifies a shared secret by name. Names describe configuration,
// never its value. Providers must not treat keys as filesystem paths.
type Key struct {
	Name string `json:"name"`
}

func (key Key) Validate() error {
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
