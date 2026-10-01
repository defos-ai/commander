// Package comms defines transport-independent bus and subscription contracts.
package comms

import (
	"context"
	"errors"

	"commander/framework/comms/messages"
)

var (
	ErrClosed          = errors.New("message bus or subscription is closed")
	ErrDeliverySettled = errors.New("delivery already settled with a different outcome")
)

// Delivery identifies one received copy independently of Message.ID. Ack confirms
// processing; Reject records failed processing. Repeating a successful settlement
// is harmless; attempting the opposite returns ErrDeliverySettled. Failed
// settlement may be retried. Retention and redelivery belong to the provider.
type Delivery interface {
	Message() messages.Message
	Ack(context.Context) error
	Reject(context.Context) error
}

// Subscription delivers a subscriber's FIFO stream. Next's context cancels one
// receive without invalidating earlier deliveries. Close ends delivery without
// acknowledging outstanding work; retention belongs to the provider.
type Subscription interface {
	Next(context.Context) (Delivery, error)
	Close() error
}

// Bus fans messages out to current subscribers of an exact topic. Subscribe's
// context applies to creation only; Close explicitly ends a subscription.
type Bus interface {
	Publish(context.Context, string, messages.Message) error
	Subscribe(context.Context, string) (Subscription, error)
	Close() error
}
