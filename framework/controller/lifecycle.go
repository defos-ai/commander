package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/controller/workers"
)

func (c *Controller) start(list ...workers.Worker) {
	var group sync.WaitGroup
	for _, worker := range list {
		group.Go(func() {
			err := worker.Run(c.lifetime)
			if err == nil && c.lifetime.Err() == nil {
				err = fmt.Errorf("%T stopped while the controller was running", worker)
			}
			c.beginClose(err)
		})
	}
	go func() {
		group.Wait()
		close(c.workersDone)
	}()
}

// Done closes when coordination stops, including event or lease processing failures.
// Hosts must still call Close to join workers and wait for subscription cleanup.
func (c *Controller) Done() <-chan struct{} { return c.lifetime.Done() }

func (c *Controller) Err() error { return context.Cause(c.lifetime) }

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
