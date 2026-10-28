package fakes

import (
	"bytes"
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type SecretBus struct {
	topics   []string
	messages []messages.Message
	err      error
}

func (bus *SecretBus) Publish(_ context.Context, topic string, message messages.Message) error {
	bus.topics = append(bus.topics, topic)
	bus.messages = append(bus.messages, message)
	return bus.err
}

func (*SecretBus) Subscribe(context.Context, comms.Inbox) (comms.Subscription, error) {
	return nil, errors.New("subscription is outside this protocol test")
}

func (*SecretBus) Close() error { return nil }

func (bus *SecretBus) Messages() []messages.Message {
	copy := make([]messages.Message, len(bus.messages))
	for i, m := range bus.messages {
		m.Payload = bytes.Clone(m.Payload)
		copy[i] = m
	}
	return copy
}
func (bus *SecretBus) Topics() []string { return append([]string(nil), bus.topics...) }
func (bus *SecretBus) Fail(err error)   { bus.err = err }
