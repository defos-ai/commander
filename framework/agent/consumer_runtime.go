package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/agent/environment"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type agentRuntime struct {
	agent       *Agent
	events      controllerevents.Client
	checkpoints *checkpointClient
	logs        *logClient
	shutdown    func(context.Context, string) error
}

func (r agentRuntime) Execute(ctx context.Context, call ActionCall, commandID string) (ActionOutput, error) {
	ctx = context.WithValue(ctx, checkpointClientKey{}, r.checkpoints)
	ctx = context.WithValue(ctx, logClientKey{}, r.logs)
	return r.agent.work(ctx, call, commandID, true, func(ctx context.Context, snapshot AgentSnapshot) error {
		publication, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		// These observations must not resolve the pending command. Its result
		// carries the correlation ID; the snapshot carries the current work ID.
		if err := r.events.State(publication, controllerevents.AgentUpdated, snapshot, ""); err != nil {
			// Reporting failure cannot turn completed side effects into a failed
			// action. Heartbeats and shutdown also publish the current snapshot.
			r.agent.config.Logger.Warn("work state announcement failed", "command_id", commandID,
				"status", snapshot.Status, "error_type", fmt.Sprintf("%T", err))
		}
		return nil
	})
}

func (r agentRuntime) Discover(ctx context.Context, scope *environment.DiscoveryScope) error {
	return r.agent.discover(ctx, scope, true)
}

func (r agentRuntime) PublishState(ctx context.Context, kind messages.MessageType, correlationID string) error {
	return r.agent.publishState(ctx, r.events, kind, correlationID)
}

func (r agentRuntime) Shutdown(ctx context.Context, commandID string) error {
	return r.shutdown(ctx, commandID)
}
