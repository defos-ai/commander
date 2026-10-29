package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/actions"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

// Only this loop mutates coordination state. Remote action waits run in callers;
// the event consumer submits one delivery and waits for processing before ACK.
func (c *Controller) run() {
	defer close(c.loopDone)
	defer c.state.Close()
	ticker := time.NewTicker(c.leaseSweepInterval)
	defer ticker.Stop()
	for c.state.Context.Err() == nil {
		select {
		case <-c.state.Context.Done():
		case request := <-c.requests:
			c.state.PrunePending()
			request()
		case <-ticker.C:
			if c.state.Context.Err() != nil {
				return
			}
			c.state.PrunePending()
			if err := c.expireLeases(); err != nil {
				c.beginClose(fmt.Errorf("expire agent leases: %w", err))
				return
			}
			if err := c.reconcileSecrets(); err != nil {
				c.beginClose(err)
				return
			}
		}
	}
}

func (c *Controller) consumeEvents(actions actions.ActionStore) {
	defer close(c.eventsDone)
	events := consumers.EventConsumer{
		Bus: c.bus, Inventory: c.inventory, Actions: actions, Executions: c.executionStore, Artifacts: c.artifactStore, State: c.state,
		Secrets: c.secrets, Operations: c.operationStore,
	}
	consumer := comms.Consumer{
		Subscription: c.events, Logger: c.logger,
		ContinueOnError: func(message messages.Message, err error) bool {
			return message.Type == messages.ExecutionLogged || errors.Is(err, consumers.ErrInvalidMessage)
		},
	}
	err := consumer.Consume(c.state.Context, func(ctx context.Context, message messages.Message) error {
		_, err := submit(ctx, c, func(ctx context.Context) (struct{}, error) {
			err := events.Consume(ctx, message)
			if err == nil && c.sessions != nil && (message.Type == messages.AgentStarted || message.Type == messages.AgentUpdated || message.Type == messages.AgentHeartbeat || message.Type == messages.AgentStopped) {
				err = c.sessions.Observe(ctx, runtimeSessions.Evidence{AgentID: message.AgentID})
			}
			// Stop before another command can be admitted after a failed observation.
			if err != nil && message.Type != messages.ExecutionLogged && !errors.Is(err, consumers.ErrInvalidMessage) {
				c.beginClose(err)
			}
			return struct{}{}, err
		})
		return err
	})
	c.beginClose(err)
}

func (c *Controller) reconcileSecrets() error {
	if c.secrets == nil {
		return nil
	}
	if err := c.secrets.Reconcile(c.state.Context); err != nil {
		return fmt.Errorf("reconcile secret delivery: %w", err)
	}
	return nil
}

func submit[T any](ctx context.Context, c *Controller, execute func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if err := c.state.CheckOpen(); err != nil {
		return zero, err
	}
	type response struct {
		value T
		err   error
	}
	reply := make(chan response, 1)
	request := func() {
		if err := ctx.Err(); err != nil {
			reply <- response{err: err}
			return
		}
		if err := c.state.CheckOpen(); err != nil {
			reply <- response{err: err}
			return
		}
		requestCtx, done := c.inventoryContext(ctx)
		defer done()
		value, err := execute(requestCtx)
		reply <- response{value, err}
	}
	select {
	case c.requests <- request:
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-c.state.Context.Done():
		return zero, c.state.ClosedError()
	}
	select {
	case result := <-reply:
		return result.value, result.err
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-c.state.Context.Done():
		return zero, c.state.ClosedError()
	}
}
