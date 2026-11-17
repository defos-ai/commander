package coordination

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
)

// StopAgent accepts durable intent. Only an agent observation confirms shutdown;
// the caller's deadline bounds acceptance, not the lifetime of the request.
func (c *Worker) StopAgent(ctx context.Context, agentID string) (agents.Agent, error) {
	if ctx == nil {
		return agents.Agent{}, errors.New("agent stop requires a context")
	}
	return submit(ctx, c, func(ctx context.Context) (agents.Agent, error) {
		record, err := c.requestStop(ctx, agentID)
		if err != nil {
			return agents.Agent{}, err
		}
		c.publishStop(c.state.Context, record)
		return record, nil
	})
}

func (c *Worker) StopAgents(ctx context.Context, selector agents.Selector) (agents.StopResult, error) {
	if ctx == nil {
		return agents.StopResult{}, errors.New("agent stop requires a context")
	}
	if err := selector.Validate(); err != nil {
		return agents.StopResult{}, err
	}
	return submit(ctx, c, func(ctx context.Context) (agents.StopResult, error) {
		records, err := c.inventory.List(ctx)
		if err != nil {
			return agents.StopResult{}, fmt.Errorf("select agents to stop: %w", err)
		}
		sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
		selected := make([]string, 0, len(records))
		for _, record := range records {
			if selector.Matches(record) {
				selected = append(selected, record.ID)
			}
		}
		result := agents.StopResult{Agents: []agents.Agent{}}
		for _, agentID := range selected {
			if ctx.Err() != nil || c.state.Context.Err() != nil {
				result.Errors = append(result.Errors, agents.StopError{ID: agentID, Error: "agent_stop_not_requested"})
				continue
			}
			record, err := c.requestStop(ctx, agentID)
			if err != nil {
				result.Errors = append(result.Errors, agents.StopError{ID: agentID, Error: stopFailureCode(err)})
				continue
			}
			result.Agents = append(result.Agents, record)
		}
		return result, nil
	})
}

func (c *Worker) requestStop(ctx context.Context, agentID string) (agents.Agent, error) {
	message, err := agentcommands.NewStopAgent(agentID)
	if err != nil {
		return agents.Agent{}, err
	}
	record, err := c.inventory.RequestStop(ctx, agentID, message.ID, message.CreatedAt)
	if err != nil {
		if stopFailureCode(err) == "agent_not_found" {
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
	return c.state.Snapshot(record, time.Now().UTC()), nil
}

func (c *Worker) reconcileStops(ctx context.Context) error {
	pass, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	records, err := c.inventory.List(pass)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return fmt.Errorf("read pending agent stops: %w", err)
	}
	pending := make(map[string]bool)
	for _, record := range records {
		if record.Status == agents.AgentStopRequested {
			pending[record.StopCommandID] = true
		}
	}
	for commandID := range c.stopPublished {
		if !pending[commandID] {
			delete(c.stopPublished, commandID)
		}
	}
	for _, record := range records {
		if pass.Err() != nil {
			break
		}
		c.publishStop(pass, record)
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
		if c.state.Context.Err() == nil {
			c.logger.Warn("agent stop publication will be retried", "agent_id", record.ID, "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	c.stopPublished[record.StopCommandID] = now
}

func stopFailureCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, executions.ErrIndeterminate), errors.Is(err, domain.ErrControllerClosed):
		return "agent_stop_uncertain"
	case errors.Is(err, agents.ErrUnknownAgent):
		return "agent_not_found"
	default:
		return "agent_stop_uncertain"
	}
}
