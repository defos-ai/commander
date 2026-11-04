package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
)

const TransientSecretValue = "api-transient-private-code-sentinel"
const TransientSecretActionName = "verify-transient-code"

type TransientSecretAction struct {
	*action.Base
	calls atomic.Int32
}

func NewTransientSecretAction() *TransientSecretAction {
	return &TransientSecretAction{Base: action.NewBase(actions.Action{
		Name: TransientSecretActionName, InputSchema: json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"matched":{"type":"boolean"}},"required":["matched"],"additionalProperties":false}`),
	})}
}

func (a *TransientSecretAction) Run(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	a.calls.Add(1)
	value, err := action.RequestTransientSecret(ctx, "CODE")
	defer clear(value)
	if err != nil {
		return action.Result{}, err
	}
	return action.JSONResult(struct {
		Matched bool `json:"matched"`
	}{Matched: bytes.Equal(value, []byte(TransientSecretValue))})
}

func (a *TransientSecretAction) Calls() int32 { return a.calls.Load() }

type TransientSecretBus struct {
	comms.Bus
	mu       sync.Mutex
	holding  bool
	held     map[string]messages.Message
	failNext atomic.Bool
}

func NewTransientSecretBus(bus comms.Bus) *TransientSecretBus {
	return &TransientSecretBus{Bus: bus, holding: true, held: make(map[string]messages.Message)}
}

func (b *TransientSecretBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if message.Type == agentevents.SecretTransientResponded {
		response, err := agentevents.DecodeTransientSecretResponse(message)
		if err != nil {
			return err
		}
		defer clear(response.Value)
		if response.Error == "" {
			if b.failNext.CompareAndSwap(true, false) {
				return errors.New("private publication detail: " + TransientSecretValue)
			}
			b.mu.Lock()
			if b.holding {
				if _, exists := b.held[message.ID]; !exists {
					message.Payload = bytes.Clone(message.Payload)
					b.held[message.ID] = message
				}
				b.mu.Unlock()
				return nil
			}
			b.mu.Unlock()
		}
	}
	return b.Bus.Publish(ctx, topic, message)
}

func (b *TransientSecretBus) Release(ctx context.Context) error {
	b.mu.Lock()
	b.holding = false
	held := b.held
	b.held = make(map[string]messages.Message)
	b.mu.Unlock()
	for _, message := range held {
		if err := b.Bus.Publish(ctx, messages.AgentEvents(message.AgentID), message); err != nil {
			clear(message.Payload)
			return err
		}
		clear(message.Payload)
	}
	return nil
}

func (b *TransientSecretBus) HeldCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.held)
}

func (b *TransientSecretBus) FailNextTransientPublication() { b.failNext.Store(true) }

func (b *TransientSecretBus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for deliveryID, message := range b.held {
		clear(message.Payload)
		delete(b.held, deliveryID)
	}
}
