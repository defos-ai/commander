package consumers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
)

// StopConsumer waits for pending results before shutting down the agent host.
type StopConsumer struct {
	Runtime  Runtime
	AgentID  string
	Events   controllerevents.Client
	Logger   *slog.Logger
	Outcomes *Outcomes
}

func (c StopConsumer) Consume(ctx context.Context, command messages.Message) error {
	if err := domain.Decode(command.Payload, &agentcommands.StopAgentCommand{}); err != nil {
		return reportFailure(ctx, c.Events, c.Logger, c.AgentID, command, err)
	}
	if err := commandAdmissionError(command); err != nil {
		return reportFailure(ctx, c.Events, c.Logger, c.AgentID, command, err)
	}
	c.Logger.Info("stop requested", "command_id", command.ID)
	draining, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := c.Outcomes.drain(draining)
	cancel()
	if err != nil {
		return reportFailure(ctx, c.Events, c.Logger, c.AgentID, command,
			fmt.Errorf("pending action results were not accepted; agent remains available: %w", err))
	}
	if err := commandAdmissionError(command); err != nil {
		return reportFailure(ctx, c.Events, c.Logger, c.AgentID, command, err)
	}
	return c.Runtime.Shutdown(ctx, command.ID)
}
