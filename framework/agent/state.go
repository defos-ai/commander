package agent

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/agent/environment"
)

// Server commands may arrive during automatic secret rediscovery. Wait for that
// refresh instead of rejecting work the controller selected from the last snapshot.
// Direct callers retain the existing fail-fast busy behavior.
func (a *Agent) claimAction(ctx context.Context, wait bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if wait {
		select {
		case a.actionGate <- struct{}{}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case a.actionGate <- struct{}{}:
		return nil
	default:
		return errors.New("agent is busy")
	}
}

type agentState struct {
	status  AgentStatus
	serving bool
	scope   *environment.DiscoveryScope
	work    *Work
}

// stateChange serializes in-memory status changes and their bus announcements.
func (a *Agent) stateChange(ctx context.Context, change func(*agentState) error, announce func(context.Context, AgentSnapshot) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.stateGate <- struct{}{}:
		defer func() { <-a.stateGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if change != nil {
		next := *a.state.Load()
		if err := change(&next); err != nil {
			return err
		}
		a.state.Store(&next)
	}
	if announce != nil {
		return announce(ctx, a.Describe())
	}
	return nil
}

func (a *Agent) finishState(ctx context.Context, status AgentStatus, announce func(context.Context, AgentSnapshot) error) error {
	return a.stateChange(context.WithoutCancel(ctx), func(next *agentState) error {
		next.status, next.work = status, nil
		return nil
	}, announce)
}
