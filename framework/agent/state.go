package agent

import (
	"context"
)

// stateChange serializes in-memory status changes and their bus announcements.
func (a *Agent) stateChange(ctx context.Context, change func() error, announce func(context.Context, AgentSnapshot) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.stateGate <- struct{}{}:
		defer func() { <-a.stateGate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if change != nil {
		a.mu.Lock()
		err := change()
		a.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if announce != nil {
		return announce(ctx, a.Describe())
	}
	return nil
}

func (a *Agent) finishState(ctx context.Context, status AgentStatus) error {
	return a.stateChange(context.WithoutCancel(ctx), func() error { a.status = status; return nil }, nil)
}
