package workers

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/domain"
)

const (
	idle uint32 = iota
	running
	stopped
)

type Lifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc
	state  atomic.Uint32
	done   chan struct{}
}

func NewLifecycle(parent context.Context) *Lifecycle {
	ctx, cancel := context.WithCancel(parent)
	return &Lifecycle{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func (l *Lifecycle) Context() context.Context { return l.ctx }

func (l *Lifecycle) Cancel() { l.cancel() }

func (l *Lifecycle) Run(ctx context.Context, run func(context.Context) error) error {
	if ctx == nil {
		return errors.New("worker execution requires a context")
	}
	if !l.state.CompareAndSwap(idle, running) {
		if l.state.Load() == stopped {
			return domain.ErrControllerClosed
		}
		return errors.New("worker is already running")
	}
	stop := context.AfterFunc(ctx, l.cancel)
	defer func() {
		stop()
		l.cancel()
		l.state.Store(stopped)
		close(l.done)
	}()
	if ctx.Err() != nil {
		l.cancel()
	}
	return run(l.ctx)
}

func (l *Lifecycle) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("worker shutdown requires a context")
	}
	l.cancel()
	if l.state.CompareAndSwap(idle, stopped) {
		close(l.done)
	}
	select {
	case <-l.done:
		return nil
	default:
	}
	select {
	case <-l.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
