package comms

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrUnroutable      = errors.New("RabbitMQ message has no destination inbox")
	ErrPublishRejected = errors.New("RabbitMQ rejected publication")
)

type Options struct {
	Prefetch int
}

type RabbitMQBus struct {
	address     string
	prefetch    int
	publisher   *connection
	channel     *amqp.Channel
	returns     <-chan amqp.Return
	publishGate chan struct{}
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	subs        map[*subscription]struct{}
	closeOnce   sync.Once
	closeErr    error
}

var _ comms.Bus = (*RabbitMQBus)(nil)

func NewRabbitMQBus(ctx context.Context, address string, options Options) (*RabbitMQBus, error) {
	if ctx == nil {
		return nil, errors.New("RabbitMQ bus requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.Prefetch < 0 || options.Prefetch > 65535 {
		return nil, errors.New("RabbitMQ prefetch must be between 0 and 65535")
	}
	if options.Prefetch == 0 {
		options.Prefetch = 16
	}
	uri, err := url.Parse(address)
	if err != nil || uri.Hostname() == "" || (uri.Scheme != "amqp" && uri.Scheme != "amqps") {
		return nil, errors.New("RabbitMQ URL must use amqp:// or amqps:// with a hostname")
	}
	if _, err := amqp.ParseURI(address); err != nil {
		return nil, errors.New("invalid RabbitMQ URL")
	}
	setup, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	conn, err := connect(setup, address)
	if err != nil {
		return nil, err
	}
	stop := abortOnCancel(setup, conn.socket)
	defer stop()
	channel, err := conn.Channel()
	if err == nil {
		err = declareTopology(channel)
	}
	if err == nil {
		err = channel.Confirm(false)
	}
	if err == nil {
		err = setup.Err()
	}
	if err != nil {
		_ = conn.close()
		return nil, transportError(setup, "initialize", err)
	}
	lifetime, close := context.WithCancel(context.Background())
	return &RabbitMQBus{address: address, prefetch: options.Prefetch, publisher: conn, channel: channel,
		returns: channel.NotifyReturn(make(chan amqp.Return, 1)), publishGate: make(chan struct{}, 1),
		ctx: lifetime, cancel: close, subs: make(map[*subscription]struct{})}, nil
}

func (b *RabbitMQBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if err := validateTopic(topic); err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	if len(message.Type) > 255 {
		return errors.New("RabbitMQ message type must be at most 255 bytes")
	}
	if !utf8.Valid(message.Payload) || !utf8.ValidString(string(message.Type)) {
		return errors.New("message text must be UTF-8")
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return b.publish(ctx, Exchange, topic, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: data,
		MessageId: message.ID, CorrelationId: message.CorrelationID, Type: string(message.Type), Timestamp: message.CreatedAt,
	})
}

func (b *RabbitMQBus) publish(ctx context.Context, exchange, key string, message amqp.Publishing) error {
	if ctx == nil {
		return errors.New("RabbitMQ publication requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	select {
	case b.publishGate <- struct{}{}:
		defer func() { <-b.publishGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-b.ctx.Done():
		return comms.ErrClosed
	}
	if b.ctx.Err() != nil || b.publisher.IsClosed() {
		return comms.ErrClosed
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	stop := abortOnCancel(ctx, b.publisher.socket)
	defer stop()
	confirmation, err := b.channel.PublishWithDeferredConfirmWithContext(ctx, exchange, key, true, false, message)
	if err != nil {
		return transportError(ctx, "publish", err)
	}
	confirmed, err := confirmation.WaitContext(ctx)
	if err != nil {
		// A timed-out confirmation has an unknown outcome. Do not let a late
		// return/confirmation become associated with a subsequent publication.
		_ = b.publisher.socket.Close()
		return transportError(ctx, "confirm publication", err)
	}
	// RabbitMQ sends basic.return before its confirmation. amqp091-go places
	// the return in this buffered channel before resolving the confirmation.
	select {
	case _, open := <-b.returns:
		if open {
			return ErrUnroutable
		}
		return comms.ErrClosed
	default:
	}
	if !confirmed {
		return ErrPublishRejected
	}
	return nil
}

func (b *RabbitMQBus) Close() error {
	b.closeOnce.Do(func() {
		b.cancel()
		b.mu.Lock()
		subs := make([]*subscription, 0, len(b.subs))
		for sub := range b.subs {
			subs = append(subs, sub)
		}
		b.mu.Unlock()
		for _, sub := range subs {
			b.closeErr = errors.Join(b.closeErr, sub.Close())
		}
		b.closeErr = errors.Join(b.closeErr, b.publisher.close())
	})
	return b.closeErr
}

func validateTopic(topic string) error {
	if topic == "" || strings.TrimSpace(topic) != topic || !utf8.ValidString(topic) || len(topic) > 255 {
		return errors.New("RabbitMQ topic must be nonblank UTF-8, at most 255 bytes, without surrounding whitespace")
	}
	return nil
}
