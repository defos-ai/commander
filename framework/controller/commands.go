package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentcommands "commander/framework/comms/clients/agent/commands"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	"commander/framework/domain"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/executions"
	"commander/framework/internal/state"
)

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
// the wait; an already-published command may still execute. Claims and outcomes
// remain in the controller database, so reusing a task ID never dispatches again.
func (c *Controller) RunAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	message, err := agentcommands.NewRunAction(agentID, call)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	reply, err := c.command(ctx, message, controllerevents.ActionSucceeded, false)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	output, err := c.actionResult(reply.Message, agentID, reply.Message.CorrelationID, call)
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

func (c *Controller) command(ctx context.Context, message messages.Message, expected messages.MessageType, replay bool) (state.CommandReply, error) {
	agentID, kind := message.AgentID, message.Type
	if ctx == nil {
		return state.CommandReply{}, errors.New("command requires a context")
	}
	if err := ctx.Err(); err != nil {
		return state.CommandReply{}, err
	}
	ctx, done := c.inventoryContext(ctx)
	defer done()
	if err := c.state.CheckOpen(); err != nil {
		return state.CommandReply{}, err
	}
	if kind == agentcommands.RunAction {
		// A task ID identifies its original command across controller restarts.
		// Check the journal before inventory: a completed result remains usable
		// after the agent has disappeared.
		stored, err := c.executionStore.Get(ctx, agentID, message.ID)
		if err == nil {
			return c.resumeClaimedAction(ctx, stored, message, replay)
		}
		if !errors.Is(err, executions.ErrUnknownExecution) {
			return state.CommandReply{}, fmt.Errorf("read execution claim: %w", err)
		}
	}
	var pending *state.PendingCommand
	for {
		c.state.Mu.RLock()
		revision, closed := c.state.Revision, c.state.Closed
		c.state.Mu.RUnlock()
		if closed || c.state.Context.Err() != nil {
			return state.CommandReply{}, c.state.ClosedError()
		}
		record, err := c.inventory.Get(ctx, agentID)
		if err != nil {
			return state.CommandReply{}, fmt.Errorf("read agent inventory: %w", c.inventoryError(err))
		}
		record = c.state.Snapshot(record, time.Now().UTC())
		c.state.Mu.Lock()
		if c.state.Closed || c.state.Context.Err() != nil {
			c.state.Mu.Unlock()
			return state.CommandReply{}, c.state.ClosedError()
		}
		// An observation may have arrived during the database read. Re-read
		// before registering a command, so a completed stop cannot be missed.
		if revision != c.state.Revision {
			c.state.Mu.Unlock()
			continue
		}
		available := record.Status == agentmodel.AgentReady || record.Status == agentmodel.AgentWorking
		if kind == agentcommands.DiscoverActions || kind == agentcommands.StopAgent {
			available = available || record.Status == agentmodel.AgentUnavailable
		}
		if record.Stale || !available {
			c.state.Mu.Unlock()
			return state.CommandReply{}, ErrAgentNotRunning
		}
		pending = &state.PendingCommand{AgentID: agentID, Work: message.Work, Expected: expected, Reply: make(chan state.CommandReply, 1)}
		if _, exists := c.state.Pending[message.ID]; exists {
			c.state.Mu.Unlock()
			return state.CommandReply{}, errors.New("command is already awaiting a result")
		}
		c.state.Pending[message.ID] = pending
		c.state.Mu.Unlock()
		break
	}
	defer func() {
		c.state.Mu.Lock()
		if c.state.Pending[message.ID] == pending {
			delete(c.state.Pending, message.ID)
		}
		c.state.Mu.Unlock()
	}()
	if kind == agentcommands.RunAction {
		stored, claimed, err := c.executionStore.Claim(ctx, message)
		if errors.Is(err, executions.ErrExecutionConflict) {
			// Another caller may have claimed this task between our first read
			// and Claim. Compare its durable request, including action input.
			stored, err = c.executionStore.Get(ctx, agentID, message.ID)
			claimed = false
		}
		if err != nil {
			return state.CommandReply{}, fmt.Errorf("claim execution: %w", err)
		}
		if !claimed {
			return c.resumeClaimedAction(ctx, stored, message, replay)
		}
	}
	if err := (agentcommands.Client{Bus: c.bus}).Publish(ctx, message); err != nil {
		// A transport can commit the command before its acknowledgement is
		// lost. Publication errors cannot establish that it never executed.
		return state.CommandReply{}, commandOutcomeError(kind, err)
	}
	select {
	case reply := <-pending.Reply:
		return reply, commandOutcomeError(kind, reply.Err)
	case <-ctx.Done():
		return state.CommandReply{}, commandOutcomeError(kind, c.inventoryError(ctx.Err()))
	case <-c.state.Context.Done():
		return state.CommandReply{}, commandOutcomeError(kind, c.state.ClosedError())
	}
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
		original.ActionName != call.ActionName || original.Work != call.Work || !sameActionInput(original.Input, call.Input) {
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
