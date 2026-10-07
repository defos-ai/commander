package consumers

import (
	"context"
	"log/slog"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
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
		if err := commandAdmissionError(command); err != nil {
			return reportFailure(ctx, c.Events, c.Logger, c.AgentID, command, err)
		}
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
