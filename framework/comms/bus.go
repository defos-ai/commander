// Package comms defines transport-independent bus and subscription contracts.
package comms

import (
	"context"
	"errors"

	"commander/framework/comms/messages"
)

var (
	ErrClosed           = errors.New("message bus or subscription is closed")
	ErrDeliverySettled  = errors.New("delivery already settled with a different outcome")
	ErrInboxInUse       = errors.New("inbox already has an active consumer")
	ErrUnsupportedInbox = errors.New("inbox mode is not supported by this provider")
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

// Subscription receives from an inbox. Next's context cancels one receive without
// invalidating earlier deliveries. Close ends consumption without acknowledging
// outstanding work or deleting a durable inbox.
type Subscription interface {
	Next(context.Context) (Delivery, error)
	Close() error
}

// Bus publishes to declared inboxes bound to an exact topic. Different inbox
// names receive independent copies. Subscribe declares an inbox and attaches a
// consumer; its context applies to creation only. Providers must reject modes
// they cannot implement with ErrUnsupportedInbox, never downgrade them.
type Bus interface {
	Publish(context.Context, string, messages.Message) error
	Subscribe(context.Context, Inbox) (Subscription, error)
	Close() error
}
