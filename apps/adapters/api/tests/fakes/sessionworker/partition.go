package sessionworker

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

type partitionBus struct {
	comms.Bus
	peer   string
	client *http.Client
}

func WithPartition(bus comms.Bus, peer string) comms.Bus {
	return &partitionBus{Bus: bus, peer: strings.TrimRight(peer, "/"), client: &http.Client{Timeout: time.Second}}
}

func (b *partitionBus) Publish(ctx context.Context, topic string, message messages.Message) error {
	if err := b.wait(ctx); err != nil {
		return err
	}
	return b.Bus.Publish(ctx, topic, message)
}

func (b *partitionBus) Subscribe(ctx context.Context, inbox comms.Inbox) (comms.Subscription, error) {
	subscription, err := b.Bus.Subscribe(ctx, inbox)
	if err != nil {
		return nil, err
	}
	return &partitionSubscription{Subscription: subscription, bus: b}, nil
}

func (b *partitionBus) wait(ctx context.Context) error {
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, b.peer+"/network", nil)
		if err != nil {
			return err
		}
		response, err := b.client.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		switch response.StatusCode {
		case http.StatusNoContent:
			return nil
		case http.StatusServiceUnavailable:
		default:
			return fmt.Errorf("session fixture network probe returned HTTP %d", response.StatusCode)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

type partitionSubscription struct {
	comms.Subscription
	bus     *partitionBus
	pending comms.Delivery
}

func (s *partitionSubscription) Next(ctx context.Context) (comms.Delivery, error) {
	if s.pending == nil {
		delivery, err := s.Subscription.Next(ctx)
		if err != nil {
			return nil, err
		}
		s.pending = delivery
	}
	if err := s.bus.wait(ctx); err != nil {
		return nil, err
	}
	delivery := s.pending
	s.pending = nil
	return delivery, nil
}
