package agent

import (
	"context"

	"commander/framework/agent/environment"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
)

// agentRuntime exposes host operations to consumers without interpreting messages.
type agentRuntime struct {
	agent    *Agent
	events   controllerevents.Client
	shutdown func(context.Context, string) error
}

func (r agentRuntime) Execute(ctx context.Context, call ActionCall, commandID string) (ActionOutput, error) {
	return r.agent.work(ctx, call, commandID, true)
}

func (r agentRuntime) Discover(ctx context.Context, scope *environment.DiscoveryScope) error {
	return r.agent.discover(ctx, scope, true)
}

func (r agentRuntime) PublishState(ctx context.Context, kind messages.MessageType, correlationID string) error {
	return r.agent.publishState(ctx, r.events, kind, correlationID)
}

func (r agentRuntime) Shutdown(ctx context.Context, commandID string) error {
	return r.shutdown(ctx, commandID)
}
