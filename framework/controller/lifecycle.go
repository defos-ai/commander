package controller

import (
	"context"
	"errors"
	"sync"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/domain/actions"
)

func (c *Controller) start(actions actions.ActionStore) {
	var workers sync.WaitGroup
	workers.Go(c.reconcileSessionControls)
	workers.Go(c.run)
	workers.Go(c.sharing.Run)
	workers.Go(func() { c.consumeEvents(actions) })
	go func() {
		workers.Wait()
		close(c.workersDone)
	}()
}

// Done closes when coordination stops, including event or lease processing failures.
// Hosts must still call Close to join workers and wait for subscription cleanup.
func (c *Controller) Done() <-chan struct{} { return c.state.Context.Done() }

func (c *Controller) Err() error { return context.Cause(c.state.Context) }

// Close wakes controller callers and releases its subscription. It leaves
// independently hosted agents and the caller-owned bus running.
func (c *Controller) Close(ctx context.Context) error {
	c.beginClose(ErrControllerClosed)
	select {
	case <-c.workersDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-c.cleanupDone:
		return c.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Controller) beginClose(cause error) {
	c.closeOnce.Do(func() {
		// Interrupt database operations without waiting for the event consumer.
		c.cancel(errors.Join(ErrControllerClosed, cause))
		// Subscription cleanup may need a remote or filesystem lock. Keep it
		// outside the caller's Close path so its context still bounds shutdown.
		go func() {
			defer close(c.cleanupDone)
			if err := c.events.Close(); err != nil && !errors.Is(err, comms.ErrClosed) {
				c.closeErr = err
			}
		}()
	})
}
