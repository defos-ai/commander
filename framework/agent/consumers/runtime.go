package consumers

import (
	"context"

	"commander/framework/agent/environment"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
)

// Runtime provides agent operations without interpreting incoming messages.
// Consumers own payload validation, command outcomes, and response publication.
type Runtime interface {
	Execute(context.Context, actionmodel.ActionCall, string) (actionmodel.ActionOutput, error)
	Discover(context.Context, *environment.DiscoveryScope) error
	PublishState(context.Context, messages.MessageType, string) error
	Shutdown(context.Context, string) error
}
