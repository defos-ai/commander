package fakes

import (
	"bytes"
	"context"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type LogAttempt struct {
	Context context.Context
	Topic   string
	Message messages.Message
}

type LogBusConfig struct {
	Failure error
	Block   bool
}

type LogBus struct {
	*WorkBus
	config   LogBusConfig
	attempts chan LogAttempt
}

func NewLogBus(agentID string, config LogBusConfig) *LogBus {
	return &LogBus{WorkBus: NewWorkBus(agentID), config: config, attempts: make(chan LogAttempt, 8)}
}

func (b *LogBus) Attempts() <-chan LogAttempt { return b.attempts }

func (b *LogBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if message.Type == controllerevents.ExecutionLogged {
		observed := message
		observed.Payload = bytes.Clone(message.Payload)
		select {
		case b.attempts <- LogAttempt{Context: ctx, Topic: topic, Message: observed}:
		case <-ctx.Done():
			return ctx.Err()
		}
		if b.config.Failure != nil {
			return b.config.Failure
		}
		if b.config.Block {
			<-ctx.Done()
			return ctx.Err()
		}
	}
	return b.WorkBus.Publish(ctx, topic, message)
}
