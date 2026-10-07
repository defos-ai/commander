package comms

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	amqp "github.com/rabbitmq/amqp091-go"
)

type subscription struct {
	bus            *RabbitMQBus
	inbox          comms.Inbox
	connection     *connection
	deliveries     <-chan amqp.Delivery
	ctx            context.Context
	cancel         context.CancelFunc
	closeOnce      sync.Once
	closeErr       error
	pendingInvalid *delivery // owned by the sole Next caller
}

var _ comms.Subscription = (*subscription)(nil)

func (b *RabbitMQBus) Subscribe(ctx context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	if ctx == nil {
		return nil, errors.New("RabbitMQ subscription requires a context")
	}
	if err := inbox.Validate(); err != nil {
		return nil, err
	}
	if err := validateTopic(inbox.Topic); err != nil {
		return nil, err
	}
	if b.ctx.Err() != nil {
		return nil, comms.ErrClosed
	}
	setup, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	stopBus := context.AfterFunc(b.ctx, cancel)
	defer stopBus()
	conn, err := connect(setup, b.address)
	if err != nil {
		return nil, err
	}
	stop := abortOnCancel(setup, conn.socket)
	defer stop()
	channel, err := conn.Channel()
	transient := inbox.Mode == comms.TransientExclusive
	if err == nil {
		_, err = channel.QueueDeclare(QueueName(inbox), !transient, transient, transient, false, amqp.Table{"x-queue-type": "classic"})
	}
	if err == nil {
		err = channel.QueueBind(QueueName(inbox), inbox.Topic, Exchange, false, nil)
	}
	if err == nil {
		err = channel.Qos(b.prefetch, 0, false)
	}
	var deliveries <-chan amqp.Delivery
	if err == nil {
		deliveries, err = channel.Consume(QueueName(inbox), "", false, transient, false, false, nil)
	}
	if err == nil {
		err = setup.Err()
	}
	if err != nil {
		_ = conn.close()
		return nil, transportError(setup, "subscribe", err)
	}
	lifetime, close := context.WithCancel(context.Background())
	sub := &subscription{bus: b, inbox: inbox, connection: conn, deliveries: deliveries, ctx: lifetime, cancel: close}
	b.mu.Lock()
	closed := b.ctx.Err() != nil
	if !closed {
		b.subs[sub] = struct{}{}
	}
	b.mu.Unlock()
	if closed {
		close()
		_ = conn.close()
		return nil, comms.ErrClosed
	}
	return sub, nil
}

func (s *subscription) Next(ctx context.Context) (comms.Delivery, error) {
	if ctx == nil {
		return nil, errors.New("RabbitMQ receive requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.ctx.Err() != nil || s.bus.ctx.Err() != nil || s.connection.IsClosed() {
		return nil, comms.ErrClosed
	}
	if s.pendingInvalid != nil {
		return nil, s.rejectInvalid(ctx)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, comms.ErrClosed
	case raw, open := <-s.deliveries:
		if !open {
			return nil, comms.ErrClosed
		}
		delivery := &delivery{subscription: s, raw: raw, gate: make(chan struct{}, 1)}
		var message messages.Message
		err := json.Unmarshal(raw.Body, &message)
		if err == nil && !utf8.Valid(raw.Body) {
			err = errors.New("message is not UTF-8")
		}
		if err == nil {
			err = message.Validate()
		}
		if err != nil {
			// Invalid bytes cannot be given to domain consumers. Preserve them
			// as a failure before settling so they cannot poison every restart.
			s.pendingInvalid = delivery
			return nil, s.rejectInvalid(ctx)
		}
		delivery.message = message
		return delivery, nil
	}
}

func (s *subscription) rejectInvalid(ctx context.Context) error {
	delivery := s.pendingInvalid
	if err := delivery.recordFailure(ctx); err != nil {
		return err
	}
	// The receive is over. Finish this ACK with its own deadline so canceling
	// Next cannot close the consuming connection and invalidate earlier work.
	settlement, cancel := context.WithTimeout(context.WithoutCancel(ctx), operationTimeout)
	defer cancel()
	if err := delivery.acknowledge(settlement, rejected); err != nil {
		return err
	}
	s.pendingInvalid = nil
	return comms.InvalidMessage(errors.New("invalid RabbitMQ message envelope; recorded in failure queue"))
}

func (s *subscription) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.closeErr = s.connection.close()
		s.bus.mu.Lock()
		delete(s.bus.subs, s)
		s.bus.mu.Unlock()
	})
	return s.closeErr
}
