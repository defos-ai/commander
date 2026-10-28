package events

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

// Client owns routing to the controller event inbox. Callers retain
// result messages when retrying so their IDs and payloads remain unchanged.
type Client struct {
	Bus comms.Bus
}

func (c Client) Publish(ctx context.Context, message messages.Message) error {
	if ctx == nil || c.Bus == nil {
		return errors.New("controller event client requires a context and bus")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch message.Type {
	case AgentStarted, AgentHeartbeat, AgentUpdated, AgentStopped, ActionSucceeded, CommandFailed:
	case SecretSyncRequested:
		if _, err := DecodeSecretSyncRequested(message); err != nil {
			return err
		}
	case SecretInstalled:
		if _, err := DecodeSecretInstalled(message); err != nil {
			return err
		}
	case CheckpointRequested:
		if _, err := DecodeCheckpointRequest(message); err != nil {
			return err
		}
	case ExecutionLogged:
		if _, err := DecodeExecutionLog(message); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported controller event type %q", message.Type)
	}
	if err := message.Validate(); err != nil {
		return err
	}
	return c.Bus.Publish(ctx, messages.ControllerEvents(messages.DefaultControllerType), message)
}

func (c Client) State(ctx context.Context, kind messages.MessageType, description AgentSnapshot, correlationID string) error {
	switch kind {
	case AgentStarted, AgentHeartbeat, AgentUpdated, AgentStopped:
	default:
		return fmt.Errorf("unsupported agent state event type %q", kind)
	}
	message, err := messages.NewMessage(kind, description.ID, description)
	if err != nil {
		return err
	}
	message.CorrelationID = correlationID
	return c.Publish(ctx, message)
}
