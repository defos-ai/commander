package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
	"commander/framework/controller/consumers"
	"commander/framework/domain/actions"
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
		}
	}
}

func (c *Controller) consumeEvents(actions actions.ActionStore) {
	defer close(c.eventsDone)
	events := consumers.EventConsumer{
		Bus: c.bus, Inventory: c.inventory, Actions: actions, Executions: c.executionStore, State: c.state,
	}
	consumer := comms.Consumer{
		Subscription: c.events, Logger: c.logger,
		ContinueOnError: func(_ messages.Message, err error) bool {
			return errors.Is(err, consumers.ErrInvalidMessage)
		},
	}
	err := consumer.Consume(c.state.Context, func(ctx context.Context, message messages.Message) error {
		_, err := submit(ctx, c, func(ctx context.Context) (struct{}, error) {
			err := events.Consume(ctx, message)
			// Stop before another command can be admitted after a failed observation.
			if err != nil && !errors.Is(err, consumers.ErrInvalidMessage) {
				c.beginClose(err)
			}
			return struct{}{}, err
		})
		return err
	})
	c.beginClose(err)
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
