package controller

import (
	"context"
	"errors"
	"fmt"

	agentcommands "commander/framework/comms/clients/agent/commands"
	controllerevents "commander/framework/comms/clients/controller/events"
	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/executions"
	"commander/framework/internal/state"
)

// DispatchAction is for durable queued tasks. Redelivery uses the exact stored
// envelope to the same process ID: agents keep a command ledger for their entire
// lifetime, and replacements receive new IDs. Never retarget unknown work.
func (c *Controller) DispatchAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	message, err := agentcommands.NewRunAction(agentID, call)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	reply, err := c.command(ctx, message, controllerevents.ActionSucceeded, true)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	output, err := c.actionResult(ctx, reply.Message, agentID, reply.Message.CorrelationID, call)
	return output, commandOutcomeError(agentcommands.RunAction, err)
}

func (c *Controller) resumeClaimedAction(ctx context.Context, stored executions.Execution, requested messages.Message, replay bool) (state.CommandReply, error) {
	if !replay {
		return c.awaitClaimedAction(ctx, stored, requested)
	}
	current, err := submit(ctx, c, func(ctx context.Context) (executions.Execution, error) {
		return c.redeliverAction(ctx, stored, requested)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrControllerClosed) {
			err = commandOutcomeError(agentcommands.RunAction, c.inventoryError(err))
		}
		return state.CommandReply{}, err
	}

	return c.awaitClaimedAction(ctx, current, requested)
}

func (c *Controller) redeliverAction(ctx context.Context, stored executions.Execution, requested messages.Message) (executions.Execution, error) {
	if err := stored.Validate(); err != nil {
		return executions.Execution{}, err
	}
	// Claim compares logical content while preserving original creation time.
	current, _, err := c.executionStore.Claim(ctx, requested)
	if err != nil {
		return executions.Execution{}, err
	}
	if current.Result == nil {
		record, err := c.Agent(ctx, current.Command.AgentID)
		if err != nil || record.Stale || (record.Status != agentmodel.AgentReady && record.Status != agentmodel.AgentWorking) {
			return executions.Execution{}, commandOutcomeError(agentcommands.RunAction, ErrAgentNotRunning)
		}
		if err := (agentcommands.Client{Bus: c.bus}).Publish(ctx, current.Command); err != nil {
			return executions.Execution{}, commandOutcomeError(agentcommands.RunAction, fmt.Errorf("redeliver saved command: %w", err))
		}
	}
	return current, nil
}
