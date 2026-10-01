package comms

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"commander/framework/comms"
	amqp "github.com/rabbitmq/amqp091-go"
)

const operationTimeout = 30 * time.Second

type connection struct {
	*amqp.Connection
	socket net.Conn
}

func connect(ctx context.Context, address string) (*connection, error) {
	var socket net.Conn
	var stop func()
	conn, err := amqp.DialConfig(address, amqp.Config{
		Heartbeat: 10 * time.Second,
		Dial: func(network, address string) (net.Conn, error) {
			var err error
			socket, err = (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			stop = abortOnCancel(ctx, socket)
			return socket, nil
		},
	})
	if stop != nil {
		stop()
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if socket != nil {
			_ = socket.Close()
		}
		return nil, transportError(ctx, "connect", err)
	}
	return &connection{Connection: conn, socket: socket}, nil
}

// Closing the socket interrupts AMQP methods that do not accept a context.
func abortOnCancel(ctx context.Context, socket net.Conn) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = socket.Close()
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

func (c *connection) close() error {
	err := c.CloseDeadline(time.Now().Add(5 * time.Second))
	_ = c.socket.Close()
	if errors.Is(err, amqp.ErrClosed) {
		return nil
	}
	return transportError(context.Background(), "close", err)
}

func transportError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, amqp.ErrClosed) {
		return fmt.Errorf("RabbitMQ %s: %w", operation, comms.ErrClosed)
	}
	var broker *amqp.Error
	if errors.As(err, &broker) {
		if broker.Code == 405 || (broker.Code == 403 && strings.Contains(broker.Reason, "exclusive")) {
			return fmt.Errorf("RabbitMQ %s: %w", operation, comms.ErrInboxInUse)
		}
		return &connectionError{operation: operation, code: broker.Code, cause: err}
	}
	return &connectionError{operation: operation, cause: err}
}

type connectionError struct {
	operation string
	code      int
	cause     error
}

func (e *connectionError) Error() string {
	if e.code != 0 {
		return fmt.Sprintf("RabbitMQ %s failed (AMQP %d)", e.operation, e.code)
	}
	return "RabbitMQ " + e.operation + " failed"
}
func (e *connectionError) Unwrap() error { return e.cause }
