package fakes

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
)

type CountingRuntime struct{ calls int }

func (r *CountingRuntime) Execute(_ context.Context, call actions.ActionCall, _ string) (actions.ActionOutput, error) {
	r.calls++
	return actions.ActionOutput{ActionName: call.ActionName, Output: []byte(`{"observed":true}`)}, nil
}
func (*CountingRuntime) Discover(context.Context, *environment.DiscoveryScope) error      { return nil }
func (*CountingRuntime) PublishState(context.Context, messages.MessageType, string) error { return nil }
func (*CountingRuntime) Shutdown(context.Context, string) error                           { return nil }

type ResultBus struct{ results chan messages.Message }

func (b *ResultBus) Publish(ctx context.Context, _ string, message messages.Message) error {
	select {
	case b.results <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*ResultBus) Subscribe(context.Context, comms.Inbox) (comms.Subscription, error) {
	return nil, errors.New("subscriptions are unused in this fault test")
}
func (*ResultBus) Close() error { return nil }

type FlakyResultBus struct {
	ResultBus
	failed atomic.Bool
}

func (b *FlakyResultBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if b.failed.CompareAndSwap(false, true) {
		return errors.New("temporary publication failure")
	}
	return b.ResultBus.Publish(ctx, topic, message)
}

func NewResultBus() *ResultBus                        { return &ResultBus{results: make(chan messages.Message, 16)} }
func (b *ResultBus) Results() <-chan messages.Message { return b.results }
func NewFlakyResultBus() *FlakyResultBus              { return &FlakyResultBus{ResultBus: *NewResultBus()} }
func (r *CountingRuntime) Calls() int                 { return r.calls }
