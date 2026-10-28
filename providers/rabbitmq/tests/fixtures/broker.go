package fixtures

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
	rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
	amqp "github.com/rabbitmq/amqp091-go"
)

type Broker struct {
	t       *testing.T
	ctx     context.Context
	url     string
	client  *amqp.Channel
	buses   []*rabbitcomms.RabbitMQBus
	inboxes map[comms.Inbox]struct{}
}

func NewBroker(t *testing.T) *Broker {
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
	f := &Broker{t: t, ctx: ctx, url: raw, client: client, inboxes: make(map[comms.Inbox]struct{})}
	t.Cleanup(func() {
		for _, bus := range f.buses {
			if err := bus.Close(); err != nil {
				t.Errorf("close bus: %v", err)
			}
		}
		if len(f.buses) != 0 {
			f.Failures()
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

func (f *Broker) Bus(prefetch int) *rabbitcomms.RabbitMQBus {
	f.t.Helper()
	bus, err := rabbitcomms.NewRabbitMQBus(f.ctx, f.url, rabbitcomms.Options{Prefetch: prefetch})
	if err != nil {
		f.t.Fatal(err)
	}
	f.buses = append(f.buses, bus)
	return bus
}

func (f *Broker) Inbox(mode comms.InboxMode) comms.Inbox {
	f.t.Helper()
	return comms.Inbox{Topic: "provider-test." + id.New(), Name: "worker-" + id.New(), Mode: mode}
}

func (f *Broker) Subscribe(bus comms.Bus, inbox comms.Inbox) comms.Subscription {
	f.t.Helper()
	sub, err := bus.Subscribe(f.ctx, inbox)
	if err != nil {
		f.t.Fatal(err)
	}
	f.inboxes[inbox] = struct{}{}
	return sub
}

func (f *Broker) Publish(bus comms.Bus, topic string, message messages.Message) {
	f.t.Helper()
	if err := bus.Publish(f.ctx, topic, message); err != nil {
		f.t.Fatal(err)
	}
}

func Message(t *testing.T) messages.Message {
	t.Helper()
	m, err := messages.NewMessage(messages.AgentHeartbeat, "review-agent-00000001", map[string]any{"detail": "RabbitMQ round trip"})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// Inspect only this fixture's failures. Keep other messages unsettled during
// the scan, then requeue them instead of purging a shared broker queue.
func (f *Broker) Failures() []amqp.Delivery {
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

func (f *Broker) Context() context.Context { return f.ctx }

func (f *Broker) Channel() *amqp.Channel { return f.client }

func (f *Broker) TrackInbox(inbox comms.Inbox) { f.inboxes[inbox] = struct{}{} }
