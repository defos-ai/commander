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
	"github.com/defos-ai/commander/providers/rabbitmq/tests/fixtures"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRabbitMQMalformedDeliveriesAreQuarantinedWithoutBlockingTheInbox(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus := f.Bus(1)
	inbox := f.Inbox(comms.DurableShared)
	sub := f.Subscribe(bus, inbox)
	if err := f.Channel().Confirm(false); err != nil {
		t.Fatal(err)
	}
	malformed := [][]byte{[]byte("{broken JSON"), []byte(`{"id":"not-a-uuid","payload":{}}`)}
	for _, body := range malformed {
		confirmation, err := f.Channel().PublishWithDeferredConfirmWithContext(f.Context(), "", rabbitcomms.QueueName(inbox), true, false, amqp.Publishing{
			ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body,
		})
		if err != nil {
			t.Fatal(err)
		}
		if accepted, err := confirmation.WaitContext(f.Context()); err != nil || !accepted {
			t.Fatalf("inject malformed delivery: accepted=%v, error=%v", accepted, err)
		}
	}
	valid := fixtures.Message(t)
	f.Publish(bus, inbox.Topic, valid)

	for range malformed {
		if delivery, err := sub.Next(f.Context()); delivery != nil || !errors.Is(err, comms.ErrInvalidMessage) {
			t.Fatalf("malformed input returned %v, %v; want ErrInvalidMessage", delivery, err)
		}
	}
	ack(t, f.Context(), nextDelivery(t, f.Context(), sub, valid))

	failures := f.Failures()
	if len(failures) != len(malformed) {
		t.Fatalf("quarantined %d deliveries, want %d", len(failures), len(malformed))
	}
	for i, failed := range failures {
		if !bytes.Equal(failed.Body, malformed[i]) || failed.Headers["commander-queue"] != rabbitcomms.QueueName(inbox) {
			t.Fatalf("quarantine lost source or original bytes: %+v", failed)
		}
	}
	closeSubscription(t, sub)
	assertNoDelivery(t, f.Context(), f.Subscribe(bus, inbox))
}

func TestRabbitMQPublishReportsUnroutableAndCanceledOperations(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus := f.Bus(0)
	inbox := f.Inbox(comms.TransientExclusive)
	publication := fixtures.Message(t)

	if err := bus.Publish(f.Context(), inbox.Topic, publication); !errors.Is(err, rabbitcomms.ErrUnroutable) {
		t.Fatalf("publish without a declared inbox: %v", err)
	}
	sub := f.Subscribe(bus, inbox)
	canceled, cancel := context.WithCancel(f.Context())
	cancel()
	if err := bus.Publish(canceled, inbox.Topic, publication); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled publication: %v", err)
	}
	if got, err := bus.Subscribe(canceled, f.Inbox(comms.DurableShared)); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled subscription: %v, %v", got, err)
	}
	if got, err := sub.Next(canceled); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled receive: %v, %v", got, err)
	}
	invalid := publication
	invalid.Type = messages.MessageType(strings.Repeat("x", 256))
	if err := bus.Publish(f.Context(), inbox.Topic, invalid); err == nil {
		t.Fatal("publication with an oversized AMQP type was accepted")
	}
	assertNoDelivery(t, f.Context(), sub)

	// A returned, invalid, or canceled publication must not poison the next confirmation.
	f.Publish(bus, inbox.Topic, publication)
	ack(t, f.Context(), nextDelivery(t, f.Context(), sub, publication))
}

func TestRabbitMQSubscriptionContextOnlyAppliesToCreationAndCloseEndsReceives(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus := f.Bus(1)
	inbox := f.Inbox(comms.TransientExclusive)
	creation, cancel := context.WithCancel(f.Context())
	sub, err := bus.Subscribe(creation, inbox)
	if err != nil {
		t.Fatal(err)
	}
	f.TrackInbox(inbox)
	cancel()
	publication := fixtures.Message(t)
	f.Publish(bus, inbox.Topic, publication)
	ack(t, f.Context(), nextDelivery(t, f.Context(), sub, publication))
	waiting := make(chan error, 1)
	go func() {
		_, err := sub.Next(f.Context())
		waiting <- err
	}()

	closeSubscription(t, sub)

	select {
	case err := <-waiting:
		if !errors.Is(err, comms.ErrClosed) {
			t.Fatalf("receive after subscription close: %v", err)
		}
	case <-f.Context().Done():
		t.Fatal("subscription close did not unblock Next")
	}
	closeSubscription(t, sub)
}

func TestRabbitMQBusCloseInvalidatesOpenResources(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus := f.Bus(1)
	inbox := f.Inbox(comms.DurableShared)
	sub := f.Subscribe(bus, inbox)
	publication := fixtures.Message(t)
	f.Publish(bus, inbox.Topic, publication)
	delivery := nextDelivery(t, f.Context(), sub, publication)

	if err := bus.Close(); err != nil {
		t.Fatal(err)
	}

	if err := bus.Publish(f.Context(), inbox.Topic, publication); !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("publish after close: %v", err)
	}
	if got, err := bus.Subscribe(f.Context(), inbox); got != nil || !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("subscribe after close: %v, %v", got, err)
	}
	if got, err := sub.Next(f.Context()); got != nil || !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("receive after close: %v, %v", got, err)
	}
	for _, settle := range []func(context.Context) error{delivery.Ack, delivery.Reject} {
		if err := settle(f.Context()); !errors.Is(err, comms.ErrClosed) {
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
			f := fixtures.NewBroker(t)
			bus := f.Bus(2)
			inbox := f.Inbox(comms.DurableShared)
			sub := f.Subscribe(bus, inbox)
			previous := fixtures.Message(t)
			f.Publish(bus, inbox.Topic, previous)
			outstanding := nextDelivery(t, f.Context(), sub, previous)
			candidate := fixtures.Message(t)
			body, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "malformed_receive" {
				body = []byte("{unparseable input to retain through a failed transfer")
			}
			if err := f.Channel().Confirm(false); err != nil {
				t.Fatal(err)
			}
			confirmation, err := f.Channel().PublishWithDeferredConfirmWithContext(f.Context(), "", rabbitcomms.QueueName(inbox), true, false, amqp.Publishing{
				ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body, MessageId: candidate.ID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if accepted, err := confirmation.WaitContext(f.Context()); err != nil || !accepted {
				t.Fatalf("inject delivery to quarantine: accepted=%v, error=%v", accepted, err)
			}
			following := fixtures.Message(t)
			f.Publish(bus, inbox.Topic, following)
			var rejected comms.Delivery
			if scenario == "rejected_delivery" {
				rejected = nextDelivery(t, f.Context(), sub, candidate)
			}
			restore := func() error {
				return f.Channel().QueueBind(rabbitcomms.FailedQueue, "", rabbitcomms.FailedExchange, false, nil)
			}
			// This broker integration group is sequential; restore its one changed
			// shared binding even if an assertion fails before the explicit restore.
			t.Cleanup(func() {
				if err := restore(); err != nil {
					t.Errorf("restore failure queue binding: %v", err)
				}
			})
			if err := f.Channel().QueueUnbind(rabbitcomms.FailedQueue, "", rabbitcomms.FailedExchange, nil); err != nil {
				t.Fatal(err)
			}

			if rejected == nil {
				if delivery, err := sub.Next(f.Context()); delivery != nil || !errors.Is(err, rabbitcomms.ErrUnroutable) {
					t.Fatalf("failed quarantine transfer: got %v, %v; want ErrUnroutable", delivery, err)
				}
			} else if err := rejected.Reject(f.Context()); !errors.Is(err, rabbitcomms.ErrUnroutable) {
				t.Fatalf("failed rejection transfer: got %v, want ErrUnroutable", err)
			}
			ack(t, f.Context(), outstanding)
			if err := restore(); err != nil {
				t.Fatal(err)
			}

			if rejected == nil {
				if delivery, err := sub.Next(f.Context()); delivery != nil || !errors.Is(err, comms.ErrInvalidMessage) {
					t.Fatalf("retry must preserve the malformed delivery: got %v, %v", delivery, err)
				}
			} else {
				for range 2 {
					if err := rejected.Reject(f.Context()); err != nil {
						t.Fatalf("retry rejected delivery: %v", err)
					}
				}
			}
			ack(t, f.Context(), nextDelivery(t, f.Context(), sub, following))
			failures := f.Failures()
			if len(failures) != 1 || !bytes.Equal(failures[0].Body, body) {
				t.Fatalf("retry must quarantine original bytes exactly once: %+v", failures)
			}
			assertNoDelivery(t, f.Context(), sub)
		})
	}
}
