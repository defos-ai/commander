package consumers

import (
	"context"
	"errors"
	"fmt"

	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/executions"
)

// CheckpointConsumer runs in the controller owner loop. Saved dispatch claims
// establish ownership even when the agent's inventory lease has since expired.
type CheckpointConsumer struct {
	Executions executions.ExecutionStore
	Bus        comms.Bus
}

func (c CheckpointConsumer) Consume(ctx context.Context, message messages.Message) error {
	request, err := controllerevents.DecodeCheckpointRequest(message)
	if err != nil {
		return InvalidMessage(err)
	}
	response := agentevents.CheckpointResponse{CommandID: request.CommandID, Name: request.Name}
	record, err := c.Executions.Get(ctx, message.AgentID, request.CommandID)
	if errors.Is(err, executions.ErrUnknownExecution) {
		response.Error = agentevents.CheckpointUnknownExecution
		return c.respond(ctx, message, response)
	}
	if err != nil {
		return fmt.Errorf("read checkpoint execution: %w", err)
	}
	if err := checkpointOwner(record, message); err != nil {
		return err
	}
	if request.Operation == controllerevents.CheckpointSave {
		if _, err := record.RecordCheckpoint(request.Name, request.Data); err != nil {
			response.Error = checkpointError(err)
			if response.Error == "" {
				response.Error = agentevents.CheckpointInvalid
			}
			return c.respond(ctx, message, response)
		}
		if err := c.Executions.SaveCheckpoint(ctx, message.AgentID, request.CommandID, request.Name, request.Data); err != nil {
			if response.Error = checkpointError(err); response.Error != "" {
				return c.respond(ctx, message, response)
			}
			// A failed write may already have committed. Never claim success or
			// permit progress before an identical retry confirms its saved value.
			return fmt.Errorf("persist execution checkpoint: %w", err)
		}
		record, err = c.Executions.Get(ctx, message.AgentID, request.CommandID)
		if err != nil {
			return fmt.Errorf("read saved execution checkpoint: %w", err)
		}
		if err := checkpointOwner(record, message); err != nil {
			return err
		}
	}
	response.Data = record.Checkpoints[request.Name]
	if len(response.Data) == 0 {
		if request.Operation == controllerevents.CheckpointSave {
			return errors.New("saved execution checkpoint is missing")
		}
		response.Error = agentevents.CheckpointNotFound
	}
	return c.respond(ctx, message, response)
}

func checkpointOwner(record executions.Execution, request messages.Message) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("invalid checkpoint execution claim: %w", err)
	}
	command := record.Command
	if command.AgentID != request.AgentID || command.ID != request.CorrelationID || command.Work != request.Work {
		return InvalidMessage(errors.New("checkpoint request does not match its execution claim"))
	}
	var call actions.ActionCall
	if err := domain.Decode(command.Payload, &call); err != nil || call.Work != command.Work {
		return errors.New("invalid claimed checkpoint action request")
	}
	return nil
}

func checkpointError(err error) string {
	switch {
	case errors.Is(err, executions.ErrUnknownExecution):
		return agentevents.CheckpointUnknownExecution
	case errors.Is(err, executions.ErrCheckpointConflict):
		return agentevents.CheckpointConflict
	case errors.Is(err, executions.ErrExecutionCompleted):
		return agentevents.CheckpointCompleted
	default:
		return ""
	}
}

func (c CheckpointConsumer) respond(ctx context.Context, request messages.Message, response agentevents.CheckpointResponse) error {
	message, err := agentevents.NewCheckpointResponse(request, response)
	if err != nil {
		return err
	}
	if err := (agentevents.Client{Bus: c.Bus}).Publish(ctx, message); err != nil {
		return fmt.Errorf("reply to checkpoint request: %w", err)
	}
	return nil
}
