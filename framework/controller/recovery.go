package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
)

// RecoverAction waits for an already-dispatched task's durable outcome. It never
// publishes a command: an uncertain execution must not silently run twice.
func (c *Controller) RecoverAction(ctx context.Context, agentID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	if ctx == nil {
		return actionmodel.ActionOutput{}, errors.New("action recovery requires a context")
	}
	if err := call.Validate(); err != nil {
		return actionmodel.ActionOutput{}, err
	}
	if call.Work.TaskID == "" {
		return actionmodel.ActionOutput{}, errors.New("action recovery requires a task ID")
	}
	return c.recoverAction(ctx, agentID, call.Work.TaskID, call)
}

func (c *Controller) recoverAction(ctx context.Context, agentID, commandID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	if err := c.state.CheckOpen(); err != nil {
		return actionmodel.ActionOutput{}, errors.Join(err, c.state.Context.Err())
	}
	ctx, done := c.inventoryContext(ctx)
	defer done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var unavailableSince time.Time
	for {
		output, finished, err := c.executionResult(ctx, agentID, commandID, call)
		if finished || err != nil {
			return output, err
		}
		// A completed result remains usable even after its agent disappears.
		// Only inspect the lease after checking the durable execution record.
		agent, err := c.Agent(ctx, agentID)
		if err != nil && !errors.Is(err, ErrUnknownAgent) {
			return actionmodel.ActionOutput{}, recoveryOutcomeError(err)
		}
		available := err == nil && !agent.Stale && (agent.Status == agentmodel.AgentReady || agent.Status == agentmodel.AgentWorking)
		if available {
			unavailableSince = time.Time{}
		} else if unavailableSince.IsZero() {
			unavailableSince = time.Now()
		}
		// A long controller outage can expire a surviving agent's inventory
		// before its next heartbeat arrives. Give it one lease interval to
		// announce itself again, bounded by the original task's deadline.
		stopped := err == nil && agent.Status == agentmodel.AgentStopped
		if stopped || (!available && time.Since(unavailableSince) >= c.state.HeartbeatTimeout) {
			// Completion and agent shutdown can race with the first read.
			output, finished, err := c.executionResult(ctx, agentID, commandID, call)
			if finished || err != nil {
				return output, err
			}
			return actionmodel.ActionOutput{}, errors.Join(executions.ErrIndeterminate, ErrAgentNotRunning)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return actionmodel.ActionOutput{}, recoveryOutcomeError(ctx.Err())
		}
	}
}

func (c *Controller) executionResult(ctx context.Context, agentID, commandID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, bool, error) {
	record, err := c.executionStore.Get(ctx, agentID, commandID)
	if errors.Is(err, executions.ErrUnknownExecution) {
		return actionmodel.ActionOutput{}, false, nil
	}
	if err != nil {
		return actionmodel.ActionOutput{}, false, recoveryOutcomeError(fmt.Errorf("read execution outcome: %w", err))
	}
	if err := record.Validate(); err != nil {
		return actionmodel.ActionOutput{}, false, recoveryOutcomeError(fmt.Errorf("invalid execution outcome: %w", err))
	}
	command := record.Command
	if command.ID != commandID || command.AgentID != agentID || command.Type != agentcommands.RunAction || command.Work != call.Work {
		return actionmodel.ActionOutput{}, false, recoveryOutcomeError(errors.New("stored command does not match the recovering task"))
	}
	var storedCall actionmodel.ActionCall
	if err := domain.Decode(command.Payload, &storedCall); err != nil {
		return actionmodel.ActionOutput{}, false, recoveryOutcomeError(err)
	}
	if storedCall.ActionName != call.ActionName || storedCall.Work != call.Work || !sameActionInput(storedCall.Input, call.Input) || !reflect.DeepEqual(storedCall.Assessment, call.Assessment) {
		return actionmodel.ActionOutput{}, false, recoveryOutcomeError(errors.New("stored action request does not match the recovering task"))
	}
	if record.Result == nil {
		return actionmodel.ActionOutput{}, false, nil
	}
	output, err := c.actionResult(ctx, *record.Result, agentID, command.ID, call)
	var failure *CommandFailure
	if err == nil || errors.As(err, &failure) {
		return output, true, err
	}
	return output, true, recoveryOutcomeError(err)
}

func (c *Controller) actionResult(ctx context.Context, message messages.Message, agentID, commandID string, call actionmodel.ActionCall) (actionmodel.ActionOutput, error) {
	output, err := executions.DecodeActionResult(message, agentID, commandID, call)
	if err != nil {
		return actionmodel.ActionOutput{}, err
	}
	if err := (consumers.ArtifactConsumer{Store: c.artifactStore}).Consume(ctx, message); err != nil {
		return actionmodel.ActionOutput{}, err
	}
	return output, nil
}

func recoveryOutcomeError(err error) error {
	// Host shutdown leaves the task running so the next host can reconcile it.
	if errors.Is(err, context.Canceled) {
		return err
	}
	return errors.Join(executions.ErrIndeterminate, err)
}

func sameActionInput(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right)
	}
	var a, b any
	decodeLeft := json.NewDecoder(bytes.NewReader(left))
	decodeRight := json.NewDecoder(bytes.NewReader(right))
	decodeLeft.UseNumber()
	decodeRight.UseNumber()
	if decodeLeft.Decode(&a) != nil || decodeRight.Decode(&b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
}
