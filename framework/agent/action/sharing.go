package action

import (
	"context"
	"errors"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
)

var (
	ErrSharedDataUnavailable = errors.New("shared data requires an active controller-dispatched operation execution")
	ErrSharedDataOverflow    = errors.New("shared data buffer overflowed; received data is incomplete")
	ErrSharedDataRejected    = errors.New("invalid shared data update")
)

type SharedDataProvider interface {
	ReceiveSharedData(context.Context) (agentevents.SharedData, error)
}

type sharedDataKey struct{}
type sharedDataBinding struct{ provider SharedDataProvider }

func WithSharedData(ctx context.Context, provider SharedDataProvider) context.Context {
	return context.WithValue(ctx, sharedDataKey{}, sharedDataBinding{provider})
}

// ReceiveSharedData waits for a pushed update in this execution's memory buffer.
// It sends no request and does not acknowledge that the action processed the data.
func ReceiveSharedData(ctx context.Context) (agentevents.SharedData, error) {
	if err := ctx.Err(); err != nil {
		return agentevents.SharedData{}, err
	}
	scoped, _ := ctx.Value(sharedDataKey{}).(sharedDataBinding)
	if scoped.provider == nil {
		return agentevents.SharedData{}, ErrSharedDataUnavailable
	}
	update, err := scoped.provider.ReceiveSharedData(ctx)
	if err != nil {
		return agentevents.SharedData{}, err
	}
	if err := ctx.Err(); err != nil {
		return agentevents.SharedData{}, err
	}
	if update.Validate() != nil {
		return agentevents.SharedData{}, ErrSharedDataRejected
	}
	return update.Clone(), nil
}
