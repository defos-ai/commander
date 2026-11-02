package action

import (
	"context"
	"errors"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
)

var (
	ErrContextUnavailable = errors.New("shared context requires an active controller-dispatched operation execution")
	ErrContextOverflow    = errors.New("shared context buffer overflowed; received context is incomplete")
	ErrContextRejected    = errors.New("invalid shared context update")
)

type ContextProvider interface {
	ReceiveContext(context.Context) (agentevents.ContextUpdate, error)
}

type contextUpdatesKey struct{}
type contextUpdates struct{ provider ContextProvider }

func WithContextUpdates(ctx context.Context, provider ContextProvider) context.Context {
	return context.WithValue(ctx, contextUpdatesKey{}, contextUpdates{provider})
}

// ReceiveContext waits for a pushed update in this execution's memory buffer.
// It sends no request and does not acknowledge that the action processed the data.
func ReceiveContext(ctx context.Context) (agentevents.ContextUpdate, error) {
	if err := ctx.Err(); err != nil {
		return agentevents.ContextUpdate{}, err
	}
	scoped, _ := ctx.Value(contextUpdatesKey{}).(contextUpdates)
	if scoped.provider == nil {
		return agentevents.ContextUpdate{}, ErrContextUnavailable
	}
	update, err := scoped.provider.ReceiveContext(ctx)
	if err != nil {
		return agentevents.ContextUpdate{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentevents.ContextUpdate{}, err
	}
	if update.Validate() != nil {
		return agentevents.ContextUpdate{}, ErrContextRejected
	}
	return update.Clone(), nil
}
