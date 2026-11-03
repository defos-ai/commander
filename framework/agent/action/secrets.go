package action

import (
	"bytes"
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

// ErrSecretUnavailable also covers undeclared names; errors never include values.
var ErrSecretUnavailable = errors.New("required secret is unavailable")

var ErrSecretRequestUnsupported = errors.New("secret provider does not support this request")

// SecretProvider supplies current secret values. Callers own returned bytes.
// Agent runtimes scope providers to the current action's RequiredSecrets.
type SecretProvider interface {
	Secret(string) ([]byte, error)
}

// SecretRequester optionally supports requesting a value through the agent's
// managed secret exchange. Requesting waits for delivery or cancellation.
type SecretRequester interface {
	RequestSecret(context.Context, string) ([]byte, error)
}

// SecretOptionsRequester receives call-scoped default bytes. Copy them before
// retaining them beyond the call; the helper clears its copy on return.
type SecretOptionsRequester interface {
	RequestSecretWithOptions(context.Context, string, SecretRequestOptions) ([]byte, error)
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

// RequestSecret defaults to reusable. Clear returned bytes when finished; never
// include them in results or logs. Providers must support any requested options.
func RequestSecret(ctx context.Context, name string, options ...SecretRequestOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := secrets.ValidateName(name); err != nil {
		return nil, err
	}
	settings, err := secretRequestOptions(options, "")
	if err != nil {
		return nil, err
	}
	provider := ctx.Value(secretContextKey{})
	if provider == nil {
		return nil, ErrSecretUnavailable
	}
	var value []byte
	if requester, ok := provider.(SecretOptionsRequester); ok {
		settings.Default = bytes.Clone(settings.Default)
		defer clear(settings.Default)
		value, err = requester.RequestSecretWithOptions(ctx, name, settings)
	} else if requester, ok := provider.(SecretRequester); ok {
		if settings.Type != SecretReusable || settings.Default != nil {
			return nil, ErrSecretRequestUnsupported
		}
		value, err = requester.RequestSecret(ctx, name)
	} else {
		return nil, ErrSecretUnavailable
	}
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
		if errors.Is(err, ErrSecretRequestUnsupported) {
			return nil, ErrSecretRequestUnsupported
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

func RequestReusableSecret(ctx context.Context, name string, options ...SecretRequestOptions) ([]byte, error) {
	return requestSecretOfType(ctx, name, SecretReusable, options)
}

func RequestTransientSecret(ctx context.Context, name string, options ...SecretRequestOptions) ([]byte, error) {
	return requestSecretOfType(ctx, name, SecretTransient, options)
}

func requestSecretOfType(ctx context.Context, name string, secretType SecretType, options []SecretRequestOptions) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	settings, err := secretRequestOptions(options, secretType)
	if err != nil {
		return nil, err
	}
	return RequestSecret(ctx, name, settings)
}
