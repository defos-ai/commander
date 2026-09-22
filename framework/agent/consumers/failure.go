package consumers

import (
	"context"
	"fmt"
	"log/slog"

	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
)

// A command failure is consumed once its response is published successfully.
func reportFailure(ctx context.Context, events controllerevents.Client, logger *slog.Logger, agentID string, command messages.Message, commandErr error) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	logger.Warn("command failed", "command_id", command.ID, "command_type", command.Type, "error_type", fmt.Sprintf("%T", commandErr))
	response, err := messages.NewMessage(controllerevents.CommandFailed, agentID, controllerevents.CommandError{
		ErrorType: fmt.Sprintf("%T", commandErr), Message: commandErr.Error(),
	})
	if err != nil {
		return err
	}
	response.CorrelationID, response.Work = command.ID, command.Work
	return events.Publish(ctx, response)
}
