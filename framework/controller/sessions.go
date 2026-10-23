package controller

import (
	"context"
	"errors"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	events "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/sessions"
	runtime "github.com/defos-ai/commander/framework/sessions"
)

func (c *Controller) RuntimeSessions() *runtime.Coordinator { return c.sessions }
func (c *Controller) ControlSession(ctx context.Context, request sessions.Control) (sessions.Session, error) {
	if c.sessions == nil {
		return sessions.Session{}, errors.New("runtime session persistence is not configured")
	}
	return c.sessions.Control(ctx, request)
}
func (c *Controller) controlSessionRemote(ctx context.Context, request sessions.Control) (sessions.Session, error) {
	if request.Method == "abandon" {
		return sessions.Session{}, errors.New("abandonment is controller-owned")
	}
	message, err := messages.NewMessage(agentcommands.ControlSession, request.OwnerAgentID, request)
	if err != nil {
		return sessions.Session{}, err
	}
	if request.RequestID != "" {
		message.ID = request.RequestID
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	reply, err := c.command(bounded, message, events.AgentUpdated, false)
	if err != nil {
		return sessions.Session{}, err
	}
	for _, item := range reply.Record.Sessions {
		if item.Reference == request.Reference {
			return item.Clone(), nil
		}
	}
	return sessions.Session{}, errors.New("owner did not report the requested session")
}
func (c *Controller) reconcileSessionControls() {
	defer close(c.sessionsDone)
	if c.sessions == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.state.Context.Done():
			return
		case <-ticker.C:
		}
		err := c.sessions.ResumeControls(c.state.Context)
		if err != nil && c.state.Context.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
			c.logger.Warn("Reconcile session controls", "error", err)
		}
	}
}
