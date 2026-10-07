package comms

import (
	"bytes"
	"context"
	"errors"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	amqp "github.com/rabbitmq/amqp091-go"
)

type settlement uint8

const (
	unsettled settlement = iota
	acknowledged
	rejected
)

type delivery struct {
	subscription *subscription
	raw          amqp.Delivery
	message      messages.Message
	gate         chan struct{}
	outcome      settlement
	settled      bool
}

var _ comms.Delivery = (*delivery)(nil)

func (d *delivery) Message() messages.Message {
	message := d.message
	message.Payload = bytes.Clone(message.Payload)
	return message
}

func (d *delivery) Ack(ctx context.Context) error    { return d.settle(ctx, acknowledged) }
func (d *delivery) Reject(ctx context.Context) error { return d.settle(ctx, rejected) }

func (d *delivery) settle(ctx context.Context, outcome settlement) error {
	if ctx == nil {
		return errors.New("RabbitMQ settlement requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	select {
	case d.gate <- struct{}{}:
		defer func() { <-d.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.outcome != unsettled && d.outcome != outcome {
		return comms.ErrDeliverySettled
	}
	if d.settled {
		return nil
	}
	s := d.subscription
	if s.ctx.Err() != nil || s.bus.ctx.Err() != nil || s.connection.IsClosed() {
		return comms.ErrClosed
	}
	if outcome == rejected {
		if err := d.recordFailure(ctx); err != nil {
			return err
		}
	}
	return d.acknowledge(ctx, outcome)
}

func (d *delivery) recordFailure(ctx context.Context) error {
	if d.outcome == rejected {
		return nil
	}
	s := d.subscription
	// Confirm the failure record before acknowledging its source. Broker
	// redelivery can repeat that record if the final acknowledgement is lost.
	err := s.bus.publish(ctx, FailedExchange, "", amqp.Publishing{
		ContentType: d.raw.ContentType, DeliveryMode: amqp.Persistent, Body: d.raw.Body,
		MessageId: d.raw.MessageId, CorrelationId: d.raw.CorrelationId, Type: d.raw.Type, Timestamp: d.raw.Timestamp,
		Headers: amqp.Table{"commander-topic": s.inbox.Topic, "commander-inbox": s.inbox.Name, "commander-queue": QueueName(s.inbox)},
	})
	if err != nil {
		return err
	}
	d.outcome = rejected
	return nil
}

func (d *delivery) acknowledge(ctx context.Context, outcome settlement) error {
	s := d.subscription
	stop := abortOnCancel(ctx, s.connection.socket)
	defer stop()
	if err := d.raw.Ack(false); err != nil {
		return transportError(ctx, "settle delivery", err)
	}
	d.outcome, d.settled = outcome, true
	return nil
}
