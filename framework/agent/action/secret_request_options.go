package action

import (
	"errors"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

type SecretType = secrets.SecretType

const (
	SecretReusable  = secrets.SecretReusable
	SecretTransient = secrets.SecretTransient
)

type SecretRequestOptions struct {
	Type    SecretType
	Default []byte `json:"-"`
}

func (SecretRequestOptions) String() string   { return "secret request options (default redacted)" }
func (SecretRequestOptions) GoString() string { return "secret request options (default redacted)" }

func secretRequestOptions(options []SecretRequestOptions, requiredType SecretType) (SecretRequestOptions, error) {
	if len(options) > 1 {
		return SecretRequestOptions{}, errors.New("secret request accepts at most one options value")
	}
	var result SecretRequestOptions
	if len(options) == 1 {
		result = options[0]
	}
	if result.Type == "" {
		result.Type = requiredType
		if result.Type == "" {
			result.Type = SecretReusable
		}
	}
	if result.Type != SecretReusable && result.Type != SecretTransient {
		return SecretRequestOptions{}, errors.New("invalid secret request type")
	}
	if requiredType != "" && result.Type != requiredType {
		return SecretRequestOptions{}, errors.New("secret request type conflicts with helper")
	}
	if result.Type == SecretTransient && result.Default != nil {
		return SecretRequestOptions{}, errors.New("transient secret requests do not accept defaults")
	}
	return result, nil
}
