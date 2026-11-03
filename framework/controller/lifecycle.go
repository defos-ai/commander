package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/defos-ai/commander/framework/controller/workers"
)

func (c *Controller) start(list ...workers.Worker) {
	c.workers = append([]workers.Worker(nil), list...)
	var group sync.WaitGroup
	for _, worker := range c.workers {
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
		// Worker cleanup can outlast one Close caller; retries join this same work.
		go func() {
			defer close(c.cleanupDone)
			c.closeErr = closeWorkers(context.Background(), c.workers)
		}()
	})
}

func closeWorkers(ctx context.Context, list []workers.Worker) error {
	var group sync.WaitGroup
	errorsByWorker := make([]error, len(list))
	for i, worker := range list {
		group.Go(func() { errorsByWorker[i] = worker.Close(ctx) })
	}
	group.Wait()
	return errors.Join(errorsByWorker...)
}
