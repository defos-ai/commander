package fakes

import (
	"context"

	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/framework/agent/action"
)

type LauncherFunc func(context.Context, string, string) (runtimes.Result, error)

func (f LauncherFunc) Spawn(ctx context.Context, requestID, reference string) (runtimes.Result, error) {
	return f(ctx, requestID, reference)
}

type SecretRequester func(context.Context, string) ([]byte, error)

func (f SecretRequester) RequestSecret(ctx context.Context, name string) ([]byte, error) {
	return f(ctx, name)
}

func (f SecretRequester) Secret(string) ([]byte, error) { return nil, action.ErrSecretUnavailable }
