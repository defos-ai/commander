package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type Delivery struct {
	message           messages.Message
	ackErr, rejectErr error
	acked, rejected   int
}

func (d *Delivery) Message() messages.Message { return d.message }
func (d *Delivery) Ack(context.Context) error {
	d.acked++
	return d.ackErr
}
func (d *Delivery) Reject(context.Context) error {
	d.rejected++
	return d.rejectErr
}

type Subscription struct{ deliveries chan comms.Delivery }

func (s *Subscription) Next(ctx context.Context) (comms.Delivery, error) {
	select {
	case d := <-s.deliveries:
		return d, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*Subscription) Close() error { return nil }

func NewDelivery(message messages.Message) *Delivery { return &Delivery{message: message} }
func (d *Delivery) FailAck(err error)                { d.ackErr = err }
func (d *Delivery) FailReject(err error)             { d.rejectErr = err }
func (d *Delivery) Acknowledged() int                { return d.acked }
func (d *Delivery) Rejected() int                    { return d.rejected }
func NewSubscription(deliveries ...*Delivery) *Subscription {
	s := &Subscription{deliveries: make(chan comms.Delivery, len(deliveries))}
	for _, d := range deliveries {
		s.deliveries <- d
	}
	return s
}
func (s *Subscription) Remaining() int { return len(s.deliveries) }
