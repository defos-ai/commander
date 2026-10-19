package controller

import (
	"context"
	"errors"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	events "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

func (c *Controller) ControlSession(ctx context.Context, request sessions.Control) (sessions.Session, error) {
	if err := request.Validate(); err != nil {
		return sessions.Session{}, err
	}
	record, err := c.inventory.Get(ctx, request.OwnerAgentID)
	if err != nil {
		return sessions.Session{}, err
	}
	record = record.Snapshot(time.Now().UTC())
	var found *sessions.Session
	for _, item := range record.Sessions {
		if item.Reference == request.Reference {
			v := item.Clone()
			found = &v
			break
		}
	}
	if found == nil {
		return sessions.Session{}, errors.New("unknown session or scope")
	}
	if found.State == "closed" || found.State == "failed" {
		return *found, nil
	}
	if record.Stale || record.Status == "stopped" {
		if request.Method == "close" {
			return sessions.Session{}, errors.New("session owner unavailable; closure cannot be confirmed")
		}
		found.State, found.Reason = "lost", "Session owner is unavailable"
		return *found, nil
	}
	message, err := messages.NewMessage(agentcommands.ControlSession, request.OwnerAgentID, request)
	if err != nil {
		return sessions.Session{}, err
	}
	reply, err := c.command(ctx, message, events.AgentUpdated, false)
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
