package controller

import (
	"context"
	"errors"
)

// Public requests stop with admission; internal operation worker calls use core coordination.
func request[T any](ctx context.Context, c *Controller, execute func(context.Context) (T, error)) (T, error) {
	var zero T
	if ctx == nil {
		return zero, errors.New("controller request requires a context")
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if c.lifetime.Err() != nil {
		return zero, errors.Join(ErrControllerClosed, c.Err())
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.lifetime, cancel)
	defer stop()
	if c.lifetime.Err() != nil {
		cancel()
	}
	value, err := execute(ctx)
	if err != nil && c.lifetime.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, ErrControllerClosed)) {
		err = errors.Join(err, ErrControllerClosed, c.Err())
	}
	return value, err
}
