package rabbitmq_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
	"commander/framework/id"
	rabbitcomms "commander/providers/rabbitmq/comms"
	amqp "github.com/rabbitmq/amqp091-go"
)

type fixture struct {
	t       *testing.T
	ctx     context.Context
	url     string
	client  *amqp.Channel
	buses   []*rabbitcomms.RabbitMQBus
	inboxes map[comms.Inbox]struct{}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	raw := os.Getenv("COMMANDER_TEST_RABBITMQ_URL")
	if raw == "" {
		t.Skip("set COMMANDER_TEST_RABBITMQ_URL to run RabbitMQ provider integration tests")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	connection, err := amqp.DialConfig(raw, amqp.Config{Dial: amqp.DefaultDial(5 * time.Second)})
	if err != nil {
		t.Fatal("connect to test RabbitMQ broker failed")
	}
	client, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		t.Fatal("open RabbitMQ test channel failed")
	}
	f := &fixture{t: t, ctx: ctx, url: raw, client: client, inboxes: make(map[comms.Inbox]struct{})}
	t.Cleanup(func() {
		for _, bus := range f.buses {
			if err := bus.Close(); err != nil {
				t.Errorf("close bus: %v", err)
			}
		}
		if len(f.buses) != 0 {
			f.failures()
		}
		for inbox := range f.inboxes {
			if inbox.Mode == comms.DurableShared {
				if _, err := client.QueueDelete(rabbitcomms.QueueName(inbox), false, false, false); err != nil {
					t.Errorf("delete test inbox: %v", err)
				}
			}
		}
		_ = client.Close()
		_ = connection.Close()
	})
	return f
}

func (f *fixture) bus(prefetch int) *rabbitcomms.RabbitMQBus {
	f.t.Helper()
	bus, err := rabbitcomms.NewRabbitMQBus(f.ctx, f.url, rabbitcomms.Options{Prefetch: prefetch})
	if err != nil {
		f.t.Fatal(err)
	}
	f.buses = append(f.buses, bus)
	return bus
}

func (f *fixture) inbox(mode comms.InboxMode) comms.Inbox {
	f.t.Helper()
	return comms.Inbox{Topic: "provider-test." + id.New(), Name: "worker-" + id.New(), Mode: mode}
}

func (f *fixture) subscribe(bus comms.Bus, inbox comms.Inbox) comms.Subscription {
	f.t.Helper()
	sub, err := bus.Subscribe(f.ctx, inbox)
	if err != nil {
		f.t.Fatal(err)
	}
	f.inboxes[inbox] = struct{}{}
	return sub
}

func (f *fixture) publish(bus comms.Bus, topic string, message messages.Message) {
	f.t.Helper()
	if err := bus.Publish(f.ctx, topic, message); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) next(sub comms.Subscription, want messages.Message) comms.Delivery {
	f.t.Helper()
	delivery, err := sub.Next(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	if delivery == nil || !reflect.DeepEqual(delivery.Message(), want) {
		f.t.Fatalf("received message differs from publication: got %v, want %+v", delivery, want)
	}
	return delivery
}

func (f *fixture) empty(sub comms.Subscription) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 150*time.Millisecond)
	defer cancel()
	if delivery, err := sub.Next(ctx); !errors.Is(err, context.DeadlineExceeded) || delivery != nil {
		f.t.Fatalf("expected no delivery: got %v, %v", delivery, err)
	}
}

func message(t *testing.T) messages.Message {
	t.Helper()
	m, err := messages.NewMessage(messages.AgentHeartbeat, "review-agent-00000001", map[string]any{"detail": "RabbitMQ round trip"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func closeSubscription(t *testing.T, sub comms.Subscription) {
	t.Helper()
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
}

func ack(t *testing.T, ctx context.Context, delivery comms.Delivery) {
	t.Helper()
	if err := delivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
}

// Inspect only this fixture's failures. Keep other messages unsettled during
// the scan, then requeue them instead of purging a shared broker queue.
func (f *fixture) failures() []amqp.Delivery {
	f.t.Helper()
	queue, err := f.client.QueueInspect(rabbitcomms.FailedQueue)
	if err != nil {
		f.t.Errorf("inspect failed queue: %v", err)
		return nil
	}
	var own, unrelated []amqp.Delivery
	defer func() {
		for _, delivery := range unrelated {
			if err := delivery.Nack(false, true); err != nil {
				f.t.Errorf("restore unrelated failed message: %v", err)
			}
		}
	}()
	for range queue.Messages {
		delivery, ok, err := f.client.Get(rabbitcomms.FailedQueue, false)
		if err != nil {
			f.t.Errorf("read failed queue: %v", err)
			return own
		}
		if !ok {
			break
		}
		belongs := false
		for inbox := range f.inboxes {
			if delivery.Headers["commander-topic"] == inbox.Topic && delivery.Headers["commander-inbox"] == inbox.Name {
				belongs = true
				break
			}
		}
		if belongs {
			own = append(own, delivery)
			if err := delivery.Ack(false); err != nil {
				f.t.Errorf("remove test failure: %v", err)
			}
		} else {
			unrelated = append(unrelated, delivery)
		}
	}
	return own
}
