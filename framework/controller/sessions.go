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

func (c *Controller) ControlSession(ctx context.Context, request sessions.Control) (sessions.Session, error) {
	if c.sessions == nil {
		return sessions.Session{}, errors.New("runtime session persistence is not configured")
	}
	return c.sessions.Control(ctx, request)
}
