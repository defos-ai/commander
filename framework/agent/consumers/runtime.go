package consumers

import (
	"context"

	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

type Runtime interface {
	Execute(context.Context, actionmodel.ActionCall, string) (actionmodel.ActionOutput, error)
	Discover(context.Context, *environment.DiscoveryScope) error
	PublishState(context.Context, messages.MessageType, string) error
	Shutdown(context.Context, string) error
}
