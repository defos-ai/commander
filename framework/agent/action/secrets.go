package action

import (
	"bytes"
	"context"
	"errors"
)

// ErrSecretUnavailable also covers undeclared names; errors never include values.
var ErrSecretUnavailable = errors.New("required secret is unavailable")

// SecretProvider supplies current secret values. Callers own returned bytes.
// Agent runtimes scope providers to the current action's RequiredSecrets.
type SecretProvider interface {
	Secret(string) ([]byte, error)
}

type secretContextKey struct{}

// WithSecrets injects a provider for discovery and execution without modifying
// the process environment. Hosts normally let Agent supply the scoped provider.
func WithSecrets(ctx context.Context, provider SecretProvider) context.Context {
	return context.WithValue(ctx, secretContextKey{}, provider)
}

// Secret reads a declared secret for this action. Clear the returned bytes when
// finished; never include them in outputs, artifacts or diagnostic messages.
func Secret(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	provider, ok := ctx.Value(secretContextKey{}).(SecretProvider)
	if !ok || provider == nil {
		return nil, ErrSecretUnavailable
	}
	value, err := provider.Secret(name)
	if err != nil {
		clear(value)
		return nil, ErrSecretUnavailable
	}
	if err := ctx.Err(); err != nil {
		clear(value)
		return nil, err
	}
	copy := bytes.Clone(value)
	clear(value)
	return copy, nil
}
