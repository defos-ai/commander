package controller

import (
	"context"
	"errors"

	"commander/framework/comms"
)

// Done closes when coordination stops, including event or lease processing failures.
// Hosts must still call Close to wait for subscription cleanup.
func (c *Controller) Done() <-chan struct{} { return c.state.Context.Done() }

// Err reports why coordination stopped, or nil while it is running.
func (c *Controller) Err() error { return context.Cause(c.state.Context) }

// Close wakes controller callers and releases its subscription. It leaves
// independently hosted agents and the caller-owned bus running.
func (c *Controller) Close(ctx context.Context) error {
	c.beginClose(ErrControllerClosed)
	select {
	case <-c.eventsDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-c.loopDone:
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
