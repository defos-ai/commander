package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	agentmodel "commander/framework/domain/agents"
)

// Agents returns independent snapshots sorted by ID. Expired records are removed
// by the lease registry; stopped records remain only until their lease expires.
func (c *Controller) Agents(ctx context.Context) ([]agentmodel.Agent, error) {
	if err := c.state.CheckOpen(); err != nil {
		return nil, err
	}
	ctx, done := c.inventoryContext(ctx)
	defer done()
	records, err := c.inventory.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agent inventory: %w", c.inventoryError(err))
	}
	now := time.Now().UTC()
	result := make([]agentmodel.Agent, 0, len(records))
	for _, record := range records {
		result = append(result, c.state.Snapshot(record, now))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (c *Controller) Agent(ctx context.Context, agentID string) (agentmodel.Agent, error) {
	if err := c.state.CheckOpen(); err != nil {
		return agentmodel.Agent{}, err
	}
	ctx, done := c.inventoryContext(ctx)
	defer done()
	record, err := c.inventory.Get(ctx, agentID)
	if err != nil {
		return agentmodel.Agent{}, fmt.Errorf("read agent inventory: %w", c.inventoryError(err))
	}
	return c.state.Snapshot(record, time.Now().UTC()), nil
}

// WaitForAgent waits for an independently hosted agent to announce that it can
// accept work. It does not start an agent, and cancellation only ends this wait.
func (c *Controller) WaitForAgent(ctx context.Context, agentID string) (agentmodel.Agent, error) {
	for {
		if err := ctx.Err(); err != nil {
			return agentmodel.Agent{}, err
		}
		c.state.Mu.RLock()
		changed := c.state.Changed
		c.state.Mu.RUnlock()
		record, err := c.Agent(ctx, agentID)
		if err == nil && !record.Stale && (record.Status == agentmodel.AgentReady || record.Status == agentmodel.AgentWorking) {
			return record, nil
		}
		if err != nil && !errors.Is(err, ErrUnknownAgent) {
			return agentmodel.Agent{}, err
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return agentmodel.Agent{}, ctx.Err()
		case <-c.state.Context.Done():
			return agentmodel.Agent{}, c.state.ClosedError()
		}
	}
}

func (c *Controller) watchLeases() {
	defer close(c.leasesDone)
	ticker := time.NewTicker(c.leaseSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.state.Context.Done():
			return
		case <-ticker.C:
			if err := c.expireLeases(); err != nil {
				c.beginClose(fmt.Errorf("expire agent leases: %w", err))
				return
			}
		}
	}
}

func (c *Controller) expireLeases() error {
	c.state.ObservationMu.Lock()
	defer c.state.ObservationMu.Unlock()
	expired, err := c.leaseRegistry.Sweep(c.state.Context, time.Now().UTC())
	if len(expired) != 0 {
		c.state.Mu.Lock()
		defer c.state.Mu.Unlock()
		if !c.state.Closed {
			for _, record := range expired {
				c.state.FailAgentLocked(record.ID, ErrAgentNotRunning)
			}
			c.state.NotifyLocked()
		}
	}
	return err
}

func (c *Controller) inventoryError(err error) error {
	if c.state.Context.Err() != nil && errors.Is(err, context.Canceled) {
		return c.state.ClosedError()
	}
	return err
}

// Inventory operations stop when either their caller or the controller ends.
func (c *Controller) inventoryContext(ctx context.Context) (context.Context, func()) {
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.state.Context, cancel)
	return operation, func() { stop(); cancel() }
}
