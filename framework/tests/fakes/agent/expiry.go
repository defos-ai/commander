package fakes

import (
	"context"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
)

type ExpiryRuntime struct {
	calls   atomic.Int64
	started chan<- context.Context
	release <-chan struct{}
}

func (r *ExpiryRuntime) Execute(ctx context.Context, call actions.ActionCall, _ string) (actions.ActionOutput, error) {
	r.calls.Add(1)
	if r.started != nil {
		r.started <- ctx
		select {
		case <-r.release:
		case <-ctx.Done():
			return actions.ActionOutput{}, ctx.Err()
		}
	}
	return actions.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"observed":true}`)}, nil
}

func (r *ExpiryRuntime) Discover(context.Context, *environment.DiscoveryScope) error {
	r.calls.Add(1)
	return nil
}

func (r *ExpiryRuntime) PublishState(context.Context, messages.MessageType, string) error {
	r.calls.Add(1)
	return nil
}

func (r *ExpiryRuntime) Shutdown(context.Context, string) error {
	r.calls.Add(1)
	return nil
}

func (r *ExpiryRuntime) Calls() int64 { return r.calls.Load() }
func (r *ExpiryRuntime) Pause(started chan<- context.Context, release <-chan struct{}) {
	r.started, r.release = started, release
}
