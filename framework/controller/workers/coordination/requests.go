package coordination

import (
	"context"

	"github.com/defos-ai/commander/framework/internal/state"
)

// Apply runs synchronously on the coordination owner. Do not retain its state,
// submit recursively, or wait for remote execution/control replies inside it.
func (c *Worker) Apply(ctx context.Context, execute func(context.Context, *state.State) error) error {
	_, err := submit(ctx, c, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, execute(ctx, c.state)
	})
	return err
}

func submit[T any](ctx context.Context, c *Worker, execute func(context.Context) (T, error)) (T, error) {
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
