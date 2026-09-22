package consumers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"commander/framework/comms"
	agentcommands "commander/framework/comms/clients/agent/commands"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
)

// ActionConsumer validates action commands, executes each command once, and retains
// its result until the controller confirms durable receipt.
type ActionConsumer struct {
	Runtime  Runtime
	AgentID  string
	Events   controllerevents.Client
	Logger   *slog.Logger
	Outcomes *Outcomes
}

func (c ActionConsumer) Consume(ctx context.Context, command messages.Message) error {
	response, claimed, err := c.Outcomes.claim(command)
	if err != nil {
		// A conflicting duplicate must not execute or overwrite the outcome of
		// the original command with a second, fabricated failure response.
		c.Logger.Warn("duplicate command rejected", "command_id", command.ID, "error", err)
		return err
	}
	if !claimed {
		c.Logger.Debug("replaying command outcome", "command_id", command.ID)
		return c.deliverResult(ctx, *response)
	}

	var call agentcommands.ActionCall
	commandErr := domain.Decode(command.Payload, &call)
	if commandErr == nil && call.Work != command.Work {
		commandErr = errors.New("command work context does not match payload")
	}
	var output actionmodel.ActionOutput
	if commandErr == nil {
		output, commandErr = c.Runtime.Execute(ctx, call, command.ID)
	}
	if ctx.Err() != nil {
		// Cancellation can leave external side effects. The controller keeps an
		// unknown outcome instead of receiving a failure that suggests retry.
		return errors.Join(context.Cause(ctx), commandErr)
	}

	kind, payload := controllerevents.ActionSucceeded, any(output)
	if commandErr != nil {
		c.Logger.Warn("command failed", "command_id", command.ID, "error_type", fmt.Sprintf("%T", commandErr))
		kind = controllerevents.CommandFailed
		payload = controllerevents.CommandError{ErrorType: fmt.Sprintf("%T", commandErr), Message: commandErr.Error()}
	}
	result, err := messages.NewMessage(kind, c.AgentID, payload)
	if err != nil {
		return err
	}
	result.CorrelationID, result.Work = command.ID, command.Work
	acknowledgeCommand := !errors.Is(commandErr, actionmodel.ErrArtifactPersistence)
	c.Outcomes.complete(result, acknowledgeCommand)
	return c.deliverResult(ctx, result)
}

func (c ActionConsumer) deliverResult(ctx context.Context, response messages.Message) error {
	if err := c.Events.Publish(ctx, response); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if errors.Is(err, comms.ErrClosed) || !c.Outcomes.canAcknowledge(response.CorrelationID) {
			return err
		}
		// Retry publication independently, but keep completion in this handler.
		// The matching receipt confirms durable delivery without rerunning work.
		c.Logger.Warn("result delivery failed; waiting for receipt", "command_id", response.CorrelationID, "error_type", fmt.Sprintf("%T", err))
		return c.Outcomes.waitAccepted(ctx, response.CorrelationID)
	}
	// Artifact persistence failures retain their original command, including
	// when a duplicate command replays the already published failure outcome.
	if !c.Outcomes.canAcknowledge(response.CorrelationID) {
		return actionmodel.ErrArtifactPersistence
	}
	return nil
}

// RetryResults publishes saved outcomes independently of command execution, so
// long-running actions do not block result delivery. IDs and payloads stay stable.
func (c ActionConsumer) RetryResults(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
			for _, response := range c.Outcomes.pending() {
				publication, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := c.Events.Publish(publication, response)
				cancel()
				if ctx.Err() != nil {
					return context.Cause(ctx)
				}
				if errors.Is(err, comms.ErrClosed) {
					return err
				}
				if err != nil {
					c.Logger.Warn("result delivery retry failed", "command_id", response.CorrelationID, "error_type", fmt.Sprintf("%T", err))
				}
			}
		}
	}
}
