package action

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/domain/sessions"
)

type sessionKey struct{}

// OpenSession returns the assigned binding for resource-specific setup or reuse.
// The controller fixes its identity before dispatch; this does not open a transport.
func OpenSession(ctx context.Context) (sessions.Binding, error) {
	return sessionForMode(ctx, sessions.Open)
}

// UseSession returns the assigned binding whose resource was checked before Run.
// Providers still enforce resource state and closure when performing work.
func UseSession(ctx context.Context) (sessions.Binding, error) {
	return sessionForMode(ctx, sessions.Use)
}

func sessionForMode(ctx context.Context, mode sessions.Mode) (sessions.Binding, error) {
	if err := ctx.Err(); err != nil {
		return sessions.Binding{}, err
	}
	binding, ok := SessionFromContext(ctx)
	if !ok {
		return sessions.Binding{}, errors.New("session requires a controller-dispatched binding")
	}
	if err := binding.Validate(); err != nil {
		return sessions.Binding{}, err
	}
	if binding.Mode != mode {
		return sessions.Binding{}, fmt.Errorf("session helper requires action session_mode=%s", mode)
	}
	return binding, nil
}

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
