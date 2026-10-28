package fakes

import (
	"context"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type Announcement struct {
	Message messages.Message
	Release chan struct{}
}

type AnnouncementBus struct {
	ctx       context.Context
	published chan Announcement
}

func (b *AnnouncementBus) Publish(ctx context.Context, _ string, message messages.Message) error {
	publication := Announcement{Message: message, Release: make(chan struct{})}
	select {
	case b.published <- publication:
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-publication.Release:
		return nil
	case <-b.ctx.Done():
		return b.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*AnnouncementBus) Subscribe(context.Context, comms.Inbox) (comms.Subscription, error) {
	return idleSubscription{}, nil
}
func (*AnnouncementBus) Close() error { return nil }

type idleSubscription struct{}

func (idleSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (idleSubscription) Close() error { return nil }

func NewAnnouncementBus(ctx context.Context) *AnnouncementBus {
	return &AnnouncementBus{ctx: ctx, published: make(chan Announcement, 4)}
}
func (b *AnnouncementBus) Published() <-chan Announcement { return b.published }
