package fakes

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/agent/action"
)

type SecretRequesterFunc func(context.Context, string) ([]byte, error)

func (request SecretRequesterFunc) RequestSecret(ctx context.Context, name string) ([]byte, error) {
	return request(ctx, name)
}

func (SecretRequesterFunc) Secret(string) ([]byte, error) {
	return nil, action.ErrSecretUnavailable
}

type SecretOptionsRequesterFunc func(context.Context, string, action.SecretRequestOptions) ([]byte, error)

func (request SecretOptionsRequesterFunc) RequestSecretWithOptions(ctx context.Context, name string, options action.SecretRequestOptions) ([]byte, error) {
	return request(ctx, name, options)
}

func (SecretOptionsRequesterFunc) RequestSecret(context.Context, string) ([]byte, error) {
	return nil, errors.New("unexpected legacy request")
}

func (SecretOptionsRequesterFunc) Secret(string) ([]byte, error) {
	return nil, action.ErrSecretUnavailable
}
