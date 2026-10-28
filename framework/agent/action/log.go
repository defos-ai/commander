package action

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/executions"
)

var ErrLogsUnavailable = errors.New("execution logs require an active controller-dispatched execution")

type LogProvider interface {
	Log(context.Context, string) error
}

type logContextKey struct{}
type logContext struct{ provider LogProvider }

// WithLogs injects an execution-scoped provider. Nil removes inherited access.
func WithLogs(ctx context.Context, provider LogProvider) context.Context {
	return context.WithValue(ctx, logContextKey{}, logContext{provider})
}

func Log(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := executions.ValidateLogMessage(message); err != nil {
		return err
	}
	scoped, _ := ctx.Value(logContextKey{}).(logContext)
	if scoped.provider == nil {
		return ErrLogsUnavailable
	}
	if err := scoped.provider.Log(ctx, message); err != nil {
		return err
	}
	return ctx.Err()
}
