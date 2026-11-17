package coordination

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
)

// StopAgent accepts durable intent. Only an agent observation confirms shutdown;
// the caller's deadline bounds acceptance, not the lifetime of the request.
func (c *Worker) StopAgent(ctx context.Context, agentID string) (agents.Agent, error) {
	if ctx == nil {
		return agents.Agent{}, errors.New("agent stop requires a context")
	}
	message, err := agentcommands.NewStopAgent(agentID)
	if err != nil {
		return agents.Agent{}, err
	}
	return submit(ctx, c, func(ctx context.Context) (agents.Agent, error) {
		record, err := c.inventory.RequestStop(ctx, agentID, message.ID, message.CreatedAt)
		if err != nil {
			if errors.Is(err, agents.ErrUnknownAgent) {
				return agents.Agent{}, err
			}
			// A failed acknowledgement may follow a committed stop request.
			reconcile, cancel := context.WithTimeout(c.state.Context, 5*time.Second)
			current, readErr := c.inventory.Get(reconcile, agentID)
			cancel()
			if readErr != nil || current.Status != agents.AgentStopRequested && current.Status != agents.AgentStopped {
				c.Stop(fmt.Errorf("reconcile agent stop request: %w", errors.Join(err, readErr)))
				return agents.Agent{}, err
			}
			record = current
		}
		c.state.Notify()
		c.publishStop(c.state.Context, record)
		return c.state.Snapshot(record, time.Now().UTC()), nil
	})
}

func (c *Worker) reconcileStops(ctx context.Context) error {
	records, err := c.inventory.List(ctx)
	if err != nil {
		return fmt.Errorf("read pending agent stops: %w", err)
	}
	pending := make(map[string]bool)
	for _, record := range records {
		if record.Status == agents.AgentStopRequested {
			pending[record.StopCommandID] = true
		}
		c.publishStop(ctx, record)
	}
	for commandID := range c.stopPublished {
		if !pending[commandID] {
			delete(c.stopPublished, commandID)
		}
	}
	return nil
}

func (c *Worker) publishStop(ctx context.Context, record agents.Agent) {
	if record.Status != agents.AgentStopRequested || c.state.Snapshot(record, time.Now().UTC()).Stale {
		return
	}
	now := time.Now().UTC()
	if sent, ok := c.stopPublished[record.StopCommandID]; ok && now.Sub(sent) < c.state.HeartbeatTimeout {
		return
	}
	if err := agents.ValidateStopRequest(record.StopCommandID, record.StopRequestedAt); err != nil {
		c.Stop(fmt.Errorf("invalid saved agent stop: %w", err))
		return
	}
	command := messages.Message{ID: record.StopCommandID, Type: agentcommands.StopAgent, AgentID: record.ID,
		CreatedAt: record.StopRequestedAt, Payload: []byte(`{}`)}
	// Graceful stop queues behind accepted work; it must not expire with the
	// requesting client while that work is still running.
	publication, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := (agentcommands.Client{Bus: c.bus}).Publish(publication, command); err != nil {
		if ctx.Err() == nil {
			c.logger.Warn("agent stop publication will be retried", "agent_id", record.ID, "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	c.stopPublished[record.StopCommandID] = now
}
