package commands

import (
	"context"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

const MethodControlSession = "sessions.control"

type SessionController interface {
	ControlSession(context.Context, sessions.Control) (sessions.Session, error)
}

func (c *Client) ControlSession(ctx context.Context, request sessions.Control) (sessions.Session, error) {
	var result sessions.Session
	if err := request.Validate(); err != nil {
		return result, err
	}
	err := c.call(ctx, MethodControlSession, request, &result)
	return result, err
}
