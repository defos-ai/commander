package coordination

import (
	"context"
	"errors"
	"time"

	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	events "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

type SessionPublisher interface {
	Publish(context.Context, messages.Message, func(context.Context, messages.Message) error) error
}

func (c *Worker) ControlSessionRemote(ctx context.Context, request sessions.Control) (sessions.Session, error) {
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
	reply, err := c.command(bounded, message, events.AgentUpdated, false, nil)
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
