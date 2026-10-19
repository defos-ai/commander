package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	agentcommands "github.com/defos-ai/commander/framework/comms/clients/agent/commands"
	events "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/sessions"
)

func (a *Agent) serveSessions(ctx context.Context, bus comms.Bus, publisher events.Client, stop context.CancelCauseFunc) (<-chan struct{}, error) {
	if a.config.Sessions == nil {
		return nil, nil
	}
	sub, err := bus.Subscribe(ctx, comms.Inbox{Topic: messages.AgentControls(a.ID()), Name: a.ID(), Mode: comms.TransientExclusive})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer sub.Close()
		var workers sync.WaitGroup
		workers.Add(1)
		go func() {
			defer workers.Done()
			consumer := comms.Consumer{Subscription: sub, Logger: a.config.Logger}
			if err := consumer.Consume(ctx, func(ctx context.Context, message messages.Message) error {
				var request sessions.Control
				err := message.Validate()
				if err == nil && (message.AgentID != a.ID() || message.Type != agentcommands.ControlSession || message.Work != (messages.WorkContext{})) {
					err = errors.New("invalid session control envelope")
				}
				if err == nil {
					err = domain.Decode(message.Payload, &request)
				}
				if err == nil {
					err = request.Validate()
				}
				if err == nil && request.OwnerAgentID != a.ID() {
					err = errors.New("session owner mismatch")
				}
				if err == nil && !message.ExpiresAt.IsZero() && !time.Now().Before(message.ExpiresAt) {
					err = errors.New("session control expired")
				}
				if err == nil {
					bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
					_, err = a.config.Sessions.Control(bounded, request)
					cancel()
				}
				if err != nil {
					reply, makeErr := messages.NewMessage(events.CommandFailed, a.ID(), events.CommandError{ErrorType: "session_control", Message: err.Error()})
					if makeErr != nil {
						return makeErr
					}
					reply.CorrelationID = message.ID
					return publisher.Publish(ctx, reply)
				}
				return a.publishState(ctx, publisher, events.AgentUpdated, message.ID)
			}); err != nil && ctx.Err() == nil {
				stop(err)
			}
		}()
		defer func() { cancel(); workers.Wait() }()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-a.config.Sessions.Changes():
				if !ok {
					return
				}
				if err := a.publishState(ctx, publisher, events.AgentUpdated, ""); err != nil {
					if ctx.Err() == nil {
						stop(err)
					}
					return
				}
			}
		}
	}()
	return done, nil
}
