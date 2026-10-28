package action

import (
	"context"

	"github.com/defos-ai/commander/framework/domain/sessions"
)

type sessionKey struct{}

// SessionFromContext returns the exact framework-owned binding for this execution.
// Resource lifetime belongs to the agent instance, not this context's deadline.
func SessionFromContext(ctx context.Context) (sessions.Binding, bool) {
	v, ok := ctx.Value(sessionKey{}).(sessions.Binding)
	return v, ok
}

func WithSession(ctx context.Context, binding *sessions.Binding) context.Context {
	if binding == nil {
		return context.WithValue(ctx, sessionKey{}, false)
	}
	return context.WithValue(ctx, sessionKey{}, *binding)
}
