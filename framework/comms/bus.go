// Package comms defines transport-independent bus and subscription contracts.
package comms

import (
	"context"
	"errors"

	"commander/framework/comms/messages"
)

var (
	ErrClosed       = errors.New("message bus or subscription is closed")
	ErrNotDelivered = errors.New("message is not a known delivery for this subscription")
)

// Subscription delivers a subscriber's FIFO stream. Next's context cancels one
// receive. Next does not acknowledge processing; replay and redelivery guarantees
// belong to the bus implementation.
// Agents and controllers receive and process deliveries through Consumer.Consume.
// Next and Ack are transport primitives used by Consumer.Consume; application
// handlers return nil to acknowledge successful processing.
// Ack confirms processing of a message returned by this subscription, using its
// ID, independently of CorrelationID. The built-in buses remember 4096 recently
// acknowledged IDs for harmless retries; an older, forgotten ID returns
// ErrNotDelivered without changing any message. If an ID is published more than
// once, Ack covers its deliveries so far, never occurrences that Next has not
// returned yet. Close ends delivery without acknowledging outstanding work;
// retention and recovery after closure belong to the implementation.
type Subscription interface {
	Next(context.Context) (messages.Message, error)
	Ack(context.Context, string) error
	Close() error
}

// Bus fans messages out to current subscribers of an exact topic. Subscribe's
// context applies to creation only; Close explicitly ends a subscription.
type Bus interface {
	Publish(context.Context, string, messages.Message) error
	Subscribe(context.Context, string) (Subscription, error)
	Close() error
}
