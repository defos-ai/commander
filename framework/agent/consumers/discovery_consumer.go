package consumers

import (
	"context"
	"log/slog"

	agentcommands "commander/framework/comms/clients/agent/commands"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain"
)

type DiscoveryConsumer struct {
	Runtime Runtime
	AgentID string
	Events  controllerevents.Client
	Logger  *slog.Logger
}

func (c DiscoveryConsumer) Consume(ctx context.Context, command messages.Message) error {
	var payload agentcommands.DiscoverActionsCommand
	commandErr := domain.Decode(command.Payload, &payload)
	if commandErr == nil {
		commandErr = c.Runtime.Discover(ctx, payload.Scope)
	}
	correlationID := command.ID
	if commandErr != nil {
		correlationID = ""
	}
	if err := c.Runtime.PublishState(ctx, controllerevents.AgentUpdated, correlationID); err != nil {
		return err
	}
	if commandErr != nil {
		return reportFailure(ctx, c.Events, c.Logger, c.AgentID, command, commandErr)
	}
	return nil
}
