package consumers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"commander/framework/comms"
	agentevents "commander/framework/comms/clients/agent/events"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/artifacts"
	"commander/framework/domain/executions"
	"commander/framework/internal/state"
)

// CommandResultConsumer validates and persists action outcomes before confirming
// acceptance to the agent or completing a waiting controller call.
type CommandResultConsumer struct {
	Inventory  agentmodel.AgentStore
	Executions executions.ExecutionStore
	Artifacts  artifacts.ArtifactStore
	Bus        comms.Bus
	State      *state.State
}

func (c CommandResultConsumer) Consume(ctx context.Context, message messages.Message) error {
	receivedAt := time.Now().UTC()
	if message.CorrelationID == "" {
		return InvalidMessage(errors.New("command result must identify its command"))
	}
	stored, err := c.Executions.Get(ctx, message.AgentID, message.CorrelationID)
	if err == nil {
		return c.acceptActionResult(ctx, message, stored, receivedAt)
	}
	if !errors.Is(err, executions.ErrUnknownExecution) {
		return fmt.Errorf("read result execution: %w", err)
	}
	// Discovery and stop commands are not action executions. Their failures
	// are only valid while the matching command is awaiting a response.
	pending, pendingExists := c.State.Pending[message.CorrelationID]
	if message.Type == controllerevents.ActionSucceeded || !pendingExists || pending.AgentID != message.AgentID || pending.Expected == controllerevents.ActionSucceeded {
		return InvalidMessage(errors.New("command result has no matching execution or pending command"))
	}
	return (AgentEventConsumer{Inventory: c.Inventory, State: c.State}).Consume(ctx, message)
}

// acceptActionResult uses the durable request as ownership evidence. Inventory
// may have expired while the controller was down and is not required to accept
// the result of a command this controller already dispatched.
func (c CommandResultConsumer) acceptActionResult(ctx context.Context, message messages.Message, stored executions.Execution, receivedAt time.Time) error {
	if err := stored.Validate(); err != nil {
		return fmt.Errorf("invalid execution claim: %w", err)
	}
	command := stored.Command
	if command.AgentID != message.AgentID || command.ID != message.CorrelationID || command.Work != message.Work {
		return InvalidMessage(errors.New("action result does not match its execution claim"))
	}
	var call actionmodel.ActionCall
	if err := domain.Decode(command.Payload, &call); err != nil {
		return fmt.Errorf("decode claimed action request: %w", err)
	}
	if err := call.Validate(); err != nil || call.Work != command.Work {
		return errors.New("invalid claimed action request")
	}
	_, resultErr := executions.DecodeActionResult(message, command.AgentID, command.ID, call)
	var failure *executions.CommandFailure
	if resultErr != nil && !errors.As(resultErr, &failure) {
		return InvalidMessage(resultErr)
	}

	// The database is the acknowledgement boundary. Neither the caller nor
	// the agent receives acceptance until the terminal result is durable.
	if err := c.Executions.Complete(ctx, message); err != nil {
		if errors.Is(err, executions.ErrExecutionConflict) || errors.Is(err, executions.ErrUnknownExecution) {
			return InvalidMessage(err)
		}
		return fmt.Errorf("persist action result: %w", err)
	}
	if err := (ArtifactConsumer{Store: c.Artifacts}).Consume(ctx, message); err != nil {
		return err
	}
	if stored.Result == nil {
		if err := c.observeActionCompletion(ctx, message, receivedAt); err != nil {
			return err
		}
	}
	receipt, err := agentevents.NewResultAccepted(message)
	if err != nil {
		return err
	}
	if err := (agentevents.Client{Bus: c.Bus}).Publish(ctx, receipt); err != nil {
		return fmt.Errorf("acknowledge durable action result: %w", err)
	}
	if err := c.State.CheckOpen(); err != nil {
		return err
	}
	if pending, ok := c.State.Pending[command.ID]; ok && pending.AgentID == command.AgentID && pending.Work == command.Work && pending.Expected == controllerevents.ActionSucceeded {
		delete(c.State.Pending, command.ID)
		pending.Reply <- state.CommandReply{Message: message, Err: resultErr}
	}
	return nil
}

func (c CommandResultConsumer) observeActionCompletion(ctx context.Context, message messages.Message, receivedAt time.Time) error {
	record, err := c.Inventory.Get(ctx, message.AgentID)
	if errors.Is(err, agentmodel.ErrUnknownAgent) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read agent inventory for result: %w", err)
	}
	// Result persistence remains valid even when its inventory observation is stale.
	fresh, timestampErr := freshObservation(message.CreatedAt, receivedAt, c.State.HeartbeatTimeout)
	if !fresh || timestampErr != nil || record.Status == agentmodel.AgentStopped || !message.CreatedAt.After(record.ObservedAt) ||
		record.Work != nil && record.Work.CommandID != message.CorrelationID {
		return nil
	}
	otherCommand := false
	for commandID, pending := range c.State.Pending {
		if commandID != message.CorrelationID && pending.AgentID == message.AgentID && pending.Expected == controllerevents.ActionSucceeded {
			otherCommand = true
			break
		}
	}
	if otherCommand {
		return nil
	}
	record.Status, record.Work, record.LastSeen = agentmodel.AgentReady, nil, receivedAt
	record.ObservedAt = message.CreatedAt
	if err := c.Inventory.Upsert(ctx, record); errors.Is(err, agentmodel.ErrStaleObservation) {
		return nil
	} else if err != nil {
		return fmt.Errorf("persist agent completion: %w", err)
	}
	if c.State.CheckOpen() == nil {
		c.State.Notify()
	}
	return nil
}
