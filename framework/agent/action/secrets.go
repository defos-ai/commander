package action

import (
	"bytes"
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

// ErrSecretUnavailable also covers undeclared names; errors never include values.
var ErrSecretUnavailable = errors.New("required secret is unavailable")

// SecretProvider supplies current secret values. Callers own returned bytes.
// Agent runtimes scope providers to the current action's RequiredSecrets.
type SecretProvider interface {
	Secret(string) ([]byte, error)
}

// SecretRequester optionally supports requesting a value through the agent's
// encrypted secret exchange. Requesting waits for delivery or cancellation.
type SecretRequester interface {
	RequestSecret(context.Context, string) ([]byte, error)
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

// RequestSecret obtains a shared named secret for the running agent. It requires a
// serving agent with secret synchronization configured and may wait for operator
// input. Clear returned bytes when finished; never include them in results or logs.
func RequestSecret(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := secrets.ValidateName(name); err != nil {
		return nil, err
	}
	provider, ok := ctx.Value(secretContextKey{}).(SecretRequester)
	if !ok || provider == nil {
		return nil, ErrSecretUnavailable
	}
	value, err := provider.RequestSecret(ctx, name)
	if err != nil {
		clear(value)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
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
