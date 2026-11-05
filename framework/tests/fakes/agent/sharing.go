package fakes

import (
	"context"
	"encoding/json"
	"sync/atomic"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type SharedDataProvider func(context.Context) (agentevents.SharedData, error)

func (f SharedDataProvider) ReceiveSharedData(ctx context.Context) (agentevents.SharedData, error) {
	return f(ctx)
}

type SharingBus struct {
	*SecretBus
	pauseWorking atomic.Bool
	working      chan messages.Message
	release      chan struct{}
}

func NewSharingBus(bus *SecretBus, pauseWorking bool) *SharingBus {
	result := &SharingBus{SecretBus: bus, working: make(chan messages.Message, 1), release: make(chan struct{}, 1)}
	result.pauseWorking.Store(pauseWorking)
	return result
}

func (b *SharingBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if err := b.SecretBus.Publish(ctx, topic, message); err != nil {
		return err
	}
	if message.Type != controllerevents.AgentUpdated || !b.pauseWorking.Load() {
		return nil
	}
	var snapshot struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(message.Payload, &snapshot) != nil || snapshot.Status != "working" || !b.pauseWorking.CompareAndSwap(true, false) {
		return nil
	}
	select {
	case b.working <- message:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *SharingBus) Working() <-chan messages.Message { return b.working }

func (b *SharingBus) ReleaseWorking() {
	select {
	case b.release <- struct{}{}:
	default:
	}
}
