package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/internal/state"
)

const defaultCommandLifetime = 5 * time.Minute

type CommandFailure = executions.CommandFailure

func (c *Controller) DiscoverActions(ctx context.Context, agentID string, scope *agentmodel.DiscoveryScope) (agentmodel.Agent, error) {
	message, err := agentcommands.NewDiscoverActions(agentID, scope)
	if err != nil {
		return agentmodel.Agent{}, err
	}
	reply, err := c.command(ctx, message, controllerevents.AgentUpdated, false)
	return reply.Record, err
}

// RunAction invokes exactly the caller-selected action. Request cancellation ends
// the wait; an already-published command may still start before its admission
// deadline. Claims and outcomes remain in the controller database, so reusing a
// task ID never dispatches again.
func (c *Controller) RunAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	message, err := agentcommands.NewRunAction(agentID, call)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	reply, err := c.command(ctx, message, controllerevents.ActionSucceeded, false)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	output, err := c.actionResult(ctx, reply.Message, agentID, reply.Message.CorrelationID, call)
	return output, commandOutcomeError(agentcommands.RunAction, err)
}

// StopAgent requests a graceful stop and waits for its event. Runtime cleanup
// belongs to the agent's host and may finish after this acknowledgement.
func (c *Controller) StopAgent(ctx context.Context, agentID string) (agentmodel.Agent, error) {
	message, err := agentcommands.NewStopAgent(agentID)
	if err != nil {
		return agentmodel.Agent{}, err
	}
	reply, err := c.command(ctx, message, controllerevents.AgentStopped, false)
	return reply.Record, err
}

type commandDispatch struct {
	pending *state.PendingCommand
	stored  *executions.Execution
}

func (c *Controller) command(ctx context.Context, message messages.Message, expected messages.MessageType, replay bool) (state.CommandReply, error) {
	if ctx == nil {
		return state.CommandReply{}, errors.New("command requires a context")
	}
	if err := ctx.Err(); err != nil {
		return state.CommandReply{}, err
	}
	message.ExpiresAt, _ = ctx.Deadline()
	if message.ExpiresAt.IsZero() {
		message.ExpiresAt = message.CreatedAt.Add(defaultCommandLifetime)
	}
	waiting, done := c.inventoryContext(ctx)
	defer done()
	dispatched, err := submit(waiting, c, func(ctx context.Context) (commandDispatch, error) {
		return c.dispatchCommand(ctx, waiting, message, expected)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrControllerClosed) {
			err = commandOutcomeError(message.Type, c.inventoryError(err))
		}
		return state.CommandReply{}, err
	}
	if dispatched.stored != nil {
		return c.resumeClaimedAction(waiting, *dispatched.stored, message, replay)
	}
	select {
	case reply := <-dispatched.pending.Reply:
		return reply, commandOutcomeError(message.Type, reply.Err)
	case <-waiting.Done():
		return state.CommandReply{}, commandOutcomeError(message.Type, c.inventoryError(waiting.Err()))
	}
}

func (c *Controller) dispatchCommand(ctx, waiting context.Context, message messages.Message, expected messages.MessageType) (commandDispatch, error) {
	agentID, kind := message.AgentID, message.Type
	if kind == agentcommands.RunAction {
		// Completed work remains usable after its agent has disappeared.
		stored, err := c.executionStore.Get(ctx, agentID, message.ID)
		if err == nil {
			return commandDispatch{stored: &stored}, nil
		}
		if !errors.Is(err, executions.ErrUnknownExecution) {
			return commandDispatch{}, fmt.Errorf("read execution claim: %w", err)
		}
	}
	record, err := c.inventory.Get(ctx, agentID)
	if err != nil {
		return commandDispatch{}, fmt.Errorf("read agent inventory: %w", c.inventoryError(err))
	}
	record = c.state.Snapshot(record, time.Now().UTC())
	available := record.Status == agentmodel.AgentReady || record.Status == agentmodel.AgentWorking
	if kind == agentcommands.DiscoverActions || kind == agentcommands.StopAgent || kind == agentcommands.ControlSession {
		available = available || record.Status == agentmodel.AgentUnavailable
	}
	if record.Stale || !available {
		return commandDispatch{}, ErrAgentNotRunning
	}
	if _, exists := c.state.Pending[message.ID]; exists {
		return commandDispatch{}, errors.New("command is already awaiting a result")
	}
	if kind == agentcommands.RunAction {
		var call actionmodel.ActionCall
		if err := domain.Decode(message.Payload, &call); err != nil {
			return commandDispatch{}, err
		}
		if call.Session != nil {
			return commandDispatch{}, errors.New("session commands require coordinated preparation")
		}
		stored, claimed, err := c.executionStore.Claim(ctx, message)
		if errors.Is(err, executions.ErrExecutionConflict) {
			stored, err = c.executionStore.Get(ctx, agentID, message.ID)
			claimed = false
		}
		if err != nil {
			return commandDispatch{}, fmt.Errorf("claim execution: %w", err)
		}
		if !claimed {
			return commandDispatch{stored: &stored}, nil
		}
	}
	pending := &state.PendingCommand{Context: waiting, AgentID: agentID, Work: message.Work, Expected: expected, Reply: make(chan state.CommandReply, 1)}
	c.state.Pending[message.ID] = pending
	if err := (agentcommands.Client{Bus: c.bus}).Publish(ctx, message); err != nil {
		delete(c.state.Pending, message.ID)
		// Publication may have committed before its confirmation was lost.
		return commandDispatch{}, commandOutcomeError(kind, err)
	}
	return commandDispatch{pending: pending}, nil
}

// awaitClaimedAction observes a previously dispatched command. Its durable claim
// never authorizes republishing, even if no result has arrived yet.
func (c *Controller) awaitClaimedAction(ctx context.Context, stored executions.Execution, requested messages.Message) (state.CommandReply, error) {
	if err := stored.Validate(); err != nil {
		return state.CommandReply{}, recoveryOutcomeError(err)
	}
	var call, original actionmodel.ActionCall
	if err := domain.Decode(requested.Payload, &call); err != nil {
		return state.CommandReply{}, err
	}
	if err := domain.Decode(stored.Command.Payload, &original); err != nil {
		return state.CommandReply{}, recoveryOutcomeError(err)
	}
	if stored.Command.ID != requested.ID || stored.Command.AgentID != requested.AgentID || stored.Command.Work != requested.Work ||
		original.ActionName != call.ActionName || original.Work != call.Work || !sameActionInput(original.Input, call.Input) || !reflect.DeepEqual(original.Assessment, call.Assessment) {
		return state.CommandReply{}, executions.ErrExecutionConflict
	}
	if stored.Result == nil {
		if _, err := c.recoverAction(ctx, requested.AgentID, requested.ID, call); err != nil {
			return state.CommandReply{}, err
		}
		var err error
		stored, err = c.executionStore.Get(ctx, requested.AgentID, requested.ID)
		if err != nil {
			return state.CommandReply{}, recoveryOutcomeError(err)
		}
	}
	if stored.Result == nil {
		return state.CommandReply{}, executions.ErrIndeterminate
	}
	return state.CommandReply{Message: *stored.Result}, nil
}

// Once dispatched, losing the reply does not prove that execution failed.
func commandOutcomeError(kind messages.MessageType, err error) error {
	if err == nil || kind != agentcommands.RunAction {
		return err
	}
	var failure *CommandFailure
	if errors.As(err, &failure) {
		return err
	}
	return errors.Join(executions.ErrIndeterminate, err)
}
