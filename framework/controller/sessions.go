package controller

import (
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/domain/sessions"
	runtime "github.com/defos-ai/commander/framework/sessions"
)

func (c *Controller) RuntimeSessions() runtime.Coordinator {
	if c.sessions == nil {
		return nil
	}
	return c.sessions
}

func (c *Controller) ControlSession(ctx context.Context, control sessions.Control) (sessions.Session, error) {
	if c.sessions == nil {
		return sessions.Session{}, errors.New("runtime session persistence is not configured")
	}
	return request(ctx, c, func(ctx context.Context) (sessions.Session, error) {
		return c.sessions.Control(ctx, control)
	})
}
