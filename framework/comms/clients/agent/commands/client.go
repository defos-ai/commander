package commands

import (
	"context"
	"errors"
	"fmt"

	"commander/framework/comms"
	"commander/framework/comms/messages"
)

type Client struct {
	Bus comms.Bus
}

// Publish sends the exact prepared command to its agent. The caller reserves
// durable command IDs before publication and decides how to observe outcomes;
// this client does not retry or create replacement commands.
func (c Client) Publish(ctx context.Context, command messages.Message) error {
	if ctx == nil || c.Bus == nil {
		return errors.New("agent command client requires a context and bus")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := command.Validate(); err != nil {
		return err
	}
	switch command.Type {
	case RunAction, DiscoverActions, StopAgent:
	default:
		return fmt.Errorf("unsupported agent command %q", command.Type)
	}
	return c.Bus.Publish(ctx, messages.AgentCommands(command.AgentID), command)
}
