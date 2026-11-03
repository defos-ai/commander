package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/controller/workers/runner"
)

func (c *Controller) start(list ...workers.Worker) {
	c.workers = append([]workers.Worker(nil), list...)
	var group sync.WaitGroup
	for _, worker := range c.workers {
		group.Go(func() {
			err := worker.Run(c.coreLifetime)
			if err == nil && c.lifetime.Err() == nil {
				err = fmt.Errorf("%T stopped while the controller was running", worker)
			}
			if c.lifetime.Err() == nil || (err != nil && err != context.Canceled && err != ErrControllerClosed && err != runner.ErrClosed) {
				if worker == c.commands {
					c.beginClose(err)
				} else {
					c.fail(err)
				}
			}
		})
	}
	go func() {
		group.Wait()
		close(c.workersDone)
	}()
}

// Done closes when admission stops, including event or lease processing failures.
// Hosts must still call Close to join workers and wait for subscription cleanup.
func (c *Controller) Done() <-chan struct{} { return c.lifetime.Done() }

func (c *Controller) Err() error { return context.Cause(c.lifetime) }

// Close stops admission and joins command intake, Runner, then core workers.
// Independently hosted agents and the caller-owned bus remain running.
func (c *Controller) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("controller shutdown requires a context")
	}
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

func (c *Controller) fail(cause error) {
	if cause == nil {
		if c.lifetime.Err() != nil {
			return
		}
		cause = errors.New("controller coordination stopped unexpectedly")
	}
	// Fence public callers before core cancellation releases pending replies.
	c.cancel(errors.Join(ErrControllerClosed, cause))
	c.cancelCore(errors.Join(ErrControllerClosed, cause))
	c.beginClose(cause)
}

func (c *Controller) beginClose(cause error) {
	c.closeOnce.Do(func() {
		c.cancel(errors.Join(ErrControllerClosed, cause))
		// Worker cleanup can outlast one Close caller; retries join this same work.
		go func() {
			defer close(c.cleanupDone)
			commandErr := c.commands.Close(context.Background())
			runnerErr := c.runner.Close(context.Background())
			c.cancelCore(context.Cause(c.lifetime))
			c.closeErr = errors.Join(commandErr, runnerErr, closeWorkers(context.Background(), c.coreWorkers))
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
