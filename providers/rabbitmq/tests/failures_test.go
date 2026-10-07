package rabbitmq_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRabbitMQMalformedDeliveriesAreQuarantinedWithoutBlockingTheInbox(t *testing.T) {
	f := newFixture(t)
	bus := f.bus(1)
	inbox := f.inbox(comms.DurableShared)
	sub := f.subscribe(bus, inbox)
	if err := f.client.Confirm(false); err != nil {
		t.Fatal(err)
	}
	malformed := [][]byte{[]byte("{broken JSON"), []byte(`{"id":"not-a-uuid","payload":{}}`)}
	for _, body := range malformed {
		confirmation, err := f.client.PublishWithDeferredConfirmWithContext(f.ctx, "", rabbitcomms.QueueName(inbox), true, false, amqp.Publishing{
			ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body,
		})
		if err != nil {
			t.Fatal(err)
		}
		if accepted, err := confirmation.WaitContext(f.ctx); err != nil || !accepted {
			t.Fatalf("inject malformed delivery: accepted=%v, error=%v", accepted, err)
		}
	}
	valid := message(t)
	f.publish(bus, inbox.Topic, valid)

	for range malformed {
		if delivery, err := sub.Next(f.ctx); delivery != nil || !errors.Is(err, comms.ErrInvalidMessage) {
			t.Fatalf("malformed input returned %v, %v; want ErrInvalidMessage", delivery, err)
		}
	}
	ack(t, f.ctx, f.next(sub, valid))

	failures := f.failures()
	if len(failures) != len(malformed) {
		t.Fatalf("quarantined %d deliveries, want %d", len(failures), len(malformed))
	}
	for i, failed := range failures {
		if !bytes.Equal(failed.Body, malformed[i]) || failed.Headers["commander-queue"] != rabbitcomms.QueueName(inbox) {
			t.Fatalf("quarantine lost source or original bytes: %+v", failed)
		}
	}
	closeSubscription(t, sub)
	f.empty(f.subscribe(bus, inbox))
}

func TestRabbitMQPublishReportsUnroutableAndCanceledOperations(t *testing.T) {
	f := newFixture(t)
	bus := f.bus(0)
	inbox := f.inbox(comms.TransientExclusive)
	publication := message(t)

	if err := bus.Publish(f.ctx, inbox.Topic, publication); !errors.Is(err, rabbitcomms.ErrUnroutable) {
		t.Fatalf("publish without a declared inbox: %v", err)
	}
	sub := f.subscribe(bus, inbox)
	canceled, cancel := context.WithCancel(f.ctx)
	cancel()
	if err := bus.Publish(canceled, inbox.Topic, publication); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication: %v", err)
	}
	if got, err := bus.Subscribe(canceled, f.inbox(comms.DurableShared)); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled subscription: %v, %v", got, err)
	}
	if got, err := sub.Next(canceled); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled receive: %v, %v", got, err)
	}
	invalid := publication
	invalid.Type = messages.MessageType(strings.Repeat("x", 256))
	if err := bus.Publish(f.ctx, inbox.Topic, invalid); err == nil {
		t.Fatal("publication with an oversized AMQP type was accepted")
	}
	f.empty(sub)

	// A returned, invalid, or canceled publication must not poison the next confirmation.
	f.publish(bus, inbox.Topic, publication)
	ack(t, f.ctx, f.next(sub, publication))
}

func TestRabbitMQSubscriptionContextOnlyAppliesToCreationAndCloseEndsReceives(t *testing.T) {
	f := newFixture(t)
	bus := f.bus(1)
	inbox := f.inbox(comms.TransientExclusive)
	creation, cancel := context.WithCancel(f.ctx)
	sub, err := bus.Subscribe(creation, inbox)
	if err != nil {
		t.Fatal(err)
	}
	f.inboxes[inbox] = struct{}{}
	cancel()
	publication := message(t)
	f.publish(bus, inbox.Topic, publication)
	ack(t, f.ctx, f.next(sub, publication))
	waiting := make(chan error, 1)
	go func() {
		_, err := sub.Next(f.ctx)
		waiting <- err
	}()

	closeSubscription(t, sub)

	select {
	case err := <-waiting:
		if !errors.Is(err, comms.ErrClosed) {
			t.Fatalf("receive after subscription close: %v", err)
		}
	case <-f.ctx.Done():
		t.Fatal("subscription close did not unblock Next")
	}
	closeSubscription(t, sub)
}

func TestRabbitMQBusCloseInvalidatesOpenResources(t *testing.T) {
	f := newFixture(t)
	bus := f.bus(1)
	inbox := f.inbox(comms.DurableShared)
	sub := f.subscribe(bus, inbox)
	publication := message(t)
	f.publish(bus, inbox.Topic, publication)
	delivery := f.next(sub, publication)

	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(f.ctx, inbox.Topic, publication); !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("publish after close: %v", err)
	}
	if got, err := bus.Subscribe(f.ctx, inbox); got != nil || !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("subscribe after close: %v, %v", got, err)
	}
	if got, err := sub.Next(f.ctx); got != nil || !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("receive after close: %v, %v", got, err)
	}
	for _, settle := range []func(context.Context) error{delivery.Ack, delivery.Reject} {
		if err := settle(f.ctx); !errors.Is(err, comms.ErrClosed) {
			t.Fatalf("unsettled delivery after close: %v", err)
		}
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("repeated bus close: %v", err)
	}
	closeSubscription(t, sub)
}

func TestRabbitMQFailedQuarantineCanRetryWithoutLosingUnsettledWork(t *testing.T) {
	for _, scenario := range []string{"malformed_receive", "rejected_delivery"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			bus := f.bus(2)
			inbox := f.inbox(comms.DurableShared)
			sub := f.subscribe(bus, inbox)
			previous := message(t)
			f.publish(bus, inbox.Topic, previous)
			outstanding := f.next(sub, previous)
			candidate := message(t)
			body, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "malformed_receive" {
				body = []byte("{unparseable input to retain through a failed transfer")
			}
			if err := f.client.Confirm(false); err != nil {
				t.Fatal(err)
			}
			confirmation, err := f.client.PublishWithDeferredConfirmWithContext(f.ctx, "", rabbitcomms.QueueName(inbox), true, false, amqp.Publishing{
				ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body, MessageId: candidate.ID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if accepted, err := confirmation.WaitContext(f.ctx); err != nil || !accepted {
				t.Fatalf("inject delivery to quarantine: accepted=%v, error=%v", accepted, err)
			}
			following := message(t)
			f.publish(bus, inbox.Topic, following)
			var rejected comms.Delivery
			if scenario == "rejected_delivery" {
				rejected = f.next(sub, candidate)
			}
			restore := func() error {
				return f.client.QueueBind(rabbitcomms.FailedQueue, "", rabbitcomms.FailedExchange, false, nil)
			}
			// This broker integration group is sequential; restore its one changed
			// shared binding even if an assertion fails before the explicit restore.
			t.Cleanup(func() {
				if err := restore(); err != nil {
					t.Errorf("restore failure queue binding: %v", err)
				}
			})
			if err := f.client.QueueUnbind(rabbitcomms.FailedQueue, "", rabbitcomms.FailedExchange, nil); err != nil {
				t.Fatal(err)
			}

			if rejected == nil {
				if delivery, err := sub.Next(f.ctx); delivery != nil || !errors.Is(err, rabbitcomms.ErrUnroutable) {
					t.Fatalf("failed quarantine transfer: got %v, %v; want ErrUnroutable", delivery, err)
				}
			} else if err := rejected.Reject(f.ctx); !errors.Is(err, rabbitcomms.ErrUnroutable) {
				t.Fatalf("failed rejection transfer: got %v, want ErrUnroutable", err)
			}
			ack(t, f.ctx, outstanding)
			if err := restore(); err != nil {
				t.Fatal(err)
			}

			if rejected == nil {
				if delivery, err := sub.Next(f.ctx); delivery != nil || !errors.Is(err, comms.ErrInvalidMessage) {
					t.Fatalf("retry must preserve the malformed delivery: got %v, %v", delivery, err)
				}
			} else {
				for range 2 {
					if err := rejected.Reject(f.ctx); err != nil {
						t.Fatalf("retry rejected delivery: %v", err)
					}
				}
			}
			ack(t, f.ctx, f.next(sub, following))
			failures := f.failures()
			if len(failures) != 1 || !bytes.Equal(failures[0].Body, body) {
				t.Fatalf("retry must quarantine original bytes exactly once: %+v", failures)
			}
			f.empty(sub)
		})
	}
}
