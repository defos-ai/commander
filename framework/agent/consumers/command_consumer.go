package consumers

import (
	"context"
	"fmt"

	agentcommands "commander/framework/comms/clients/agent/commands"
	"commander/framework/comms/messages"
)

// CommandConsumer routes this agent's incoming commands to their consumers.
type CommandConsumer struct {
	AgentID   string
	Action    ActionConsumer
	Discovery DiscoveryConsumer
	Stop      StopConsumer
}

func (c CommandConsumer) Consume(ctx context.Context, command messages.Message) error {
	if command.AgentID != c.AgentID {
		return nil
	}
	switch command.Type {
	case agentcommands.RunAction:
		return c.Action.Consume(ctx, command)
	case agentcommands.DiscoverActions:
		return c.Discovery.Consume(ctx, command)
	case agentcommands.StopAgent:
		return c.Stop.Consume(ctx, command)
	default:
		return fmt.Errorf("unsupported command type %q", command.Type)
	}
}
