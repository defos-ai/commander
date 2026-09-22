package controller

import (
	"context"
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
	output, err := c.actionResult(reply.Message, agentID, reply.Message.CorrelationID, call)
	return output, commandOutcomeError(agentcommands.RunAction, err)
}

func (c *Controller) resumeClaimedAction(ctx context.Context, stored executions.Execution, requested messages.Message, replay bool) (state.CommandReply, error) {
	if !replay {
		return c.awaitClaimedAction(ctx, stored, requested)
	}
	if err := stored.Validate(); err != nil {
		return state.CommandReply{}, err
	}
	// Claim compares logical content while preserving original creation time.
	current, _, err := c.executionStore.Claim(ctx, requested)
	if err != nil {
		return state.CommandReply{}, err
	}
	stored = current
	if stored.Result == nil {
		record, err := c.Agent(ctx, stored.Command.AgentID)
		if err != nil || record.Stale || (record.Status != agentmodel.AgentReady && record.Status != agentmodel.AgentWorking) {
			return state.CommandReply{}, commandOutcomeError(agentcommands.RunAction, ErrAgentNotRunning)
		}
		if err := (agentcommands.Client{Bus: c.bus}).Publish(ctx, stored.Command); err != nil {
			return state.CommandReply{}, commandOutcomeError(agentcommands.RunAction, fmt.Errorf("redeliver saved command: %w", err))
		}
	}
	return c.awaitClaimedAction(ctx, stored, requested)
}
