package rabbitmq_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/defos-ai/commander/framework/comms"
	rabbitcomms "github.com/defos-ai/commander/providers/rabbitmq/comms"
	"github.com/defos-ai/commander/providers/rabbitmq/tests/fixtures"
)

func TestRabbitMQFansOutToNamedInboxesOnExactTopics(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus, otherBus := f.Bus(1), f.Bus(1)
	inbox := f.Inbox(comms.TransientExclusive)
	inbox.Topic += ".*"
	otherName := inbox
	otherName.Name += "-other"
	otherTopic := inbox
	otherTopic.Topic = strings.TrimSuffix(inbox.Topic, "*") + "events"
	first := f.Subscribe(bus, inbox)
	second := f.Subscribe(otherBus, otherName)
	third := f.Subscribe(otherBus, otherTopic)
	fanout, separate := fixtures.Message(t), fixtures.Message(t)

	f.Publish(bus, inbox.Topic, fanout)
	f.Publish(bus, otherTopic.Topic, separate)

	ack(t, f.Context(), nextDelivery(t, f.Context(), first, fanout))
	ack(t, f.Context(), nextDelivery(t, f.Context(), second, fanout))
	ack(t, f.Context(), nextDelivery(t, f.Context(), third, separate))
	for _, sub := range []comms.Subscription{first, second, third} {
		assertNoDelivery(t, f.Context(), sub)
	}
}

func TestRabbitMQDurableInboxSharesWorkAcrossConsumers(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus, otherBus := f.Bus(1), f.Bus(1)
	inbox := f.Inbox(comms.DurableShared)
	first := f.Subscribe(bus, inbox)
	second := f.Subscribe(otherBus, inbox)
	one, two := fixtures.Message(t), fixtures.Message(t)

	f.Publish(bus, inbox.Topic, one)
	f.Publish(bus, inbox.Topic, two)

	// With prefetch one, neither consumer can reserve both publications.
	deliveries := make([]comms.Delivery, 0, 2)
	for _, sub := range []comms.Subscription{first, second} {
		delivery, err := sub.Next(f.Context())
		if err != nil {
			t.Fatal(err)
		}
		deliveries = append(deliveries, delivery)
	}
	seen := map[string]bool{}
	for _, delivery := range deliveries {
		got := delivery.Message().ID
		if seen[got] || (got != one.ID && got != two.ID) {
			t.Fatalf("competing consumers received duplicate or unknown message %q", got)
		}
		seen[got] = true
		ack(t, f.Context(), delivery)
	}
	assertNoDelivery(t, f.Context(), first)
	assertNoDelivery(t, f.Context(), second)
}

func TestRabbitMQClosedObserverDoesNotRejectPublicationToRemainingInbox(t *testing.T) {
	f := fixtures.NewBroker(t)
	observerBus, publisher := f.Bus(1), f.Bus(1)
	inbox := f.Inbox(comms.DurableShared)
	destination := f.Subscribe(publisher, inbox)
	observerInbox := inbox
	observerInbox.Name += "-observer"
	observerInbox.Mode = comms.TransientExclusive
	observer := f.Subscribe(observerBus, observerInbox)
	registration := fixtures.Message(t)
	f.Publish(publisher, inbox.Topic, registration)
	ack(t, f.Context(), nextDelivery(t, f.Context(), observer, registration))
	ack(t, f.Context(), nextDelivery(t, f.Context(), destination, registration))

	closeSubscription(t, observer)

	ready := fixtures.Message(t)
	f.Publish(observerBus, inbox.Topic, ready)
	ack(t, f.Context(), nextDelivery(t, f.Context(), destination, ready))
}

func TestRabbitMQDurableInboxRedeliversAndRetainsWorkDuringDowntime(t *testing.T) {
	f := fixtures.NewBroker(t)
	owner, publisher := f.Bus(1), f.Bus(1)
	inbox := f.Inbox(comms.DurableShared)
	first := f.Subscribe(owner, inbox)
	unsettled := fixtures.Message(t)
	f.Publish(publisher, inbox.Topic, unsettled)
	oldDelivery := nextDelivery(t, f.Context(), first, unsettled)

	closeSubscription(t, first)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := f.Subscribe(publisher, inbox)

	ack(t, f.Context(), nextDelivery(t, f.Context(), reopened, unsettled))
	if err := oldDelivery.Ack(f.Context()); !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("old delivery settlement: got %v, want ErrClosed", err)
	}
	closeSubscription(t, reopened)
	duringDowntime := fixtures.Message(t)
	f.Publish(publisher, inbox.Topic, duringDowntime)
	afterDowntime := f.Subscribe(publisher, inbox)
	ack(t, f.Context(), nextDelivery(t, f.Context(), afterDowntime, duringDowntime))
	assertNoDelivery(t, f.Context(), afterDowntime)
}

func TestRabbitMQTransientInboxHasOneOwnerAndNoReplay(t *testing.T) {
	for _, endLifetime := range []string{"subscription_close", "connection_close"} {
		t.Run(endLifetime, func(t *testing.T) {
			f := fixtures.NewBroker(t)
			owner, other := f.Bus(1), f.Bus(1)
			inbox := f.Inbox(comms.TransientExclusive)
			original := f.Subscribe(owner, inbox)
			for _, bus := range []comms.Bus{owner, other} {
				if duplicate, err := bus.Subscribe(f.Context(), inbox); duplicate != nil || !errors.Is(err, comms.ErrInboxInUse) {
					t.Fatalf("second owner: got %v, %v, want ErrInboxInUse", duplicate, err)
				}
			}
			old := fixtures.Message(t)
			f.Publish(other, inbox.Topic, old)
			outstanding := nextDelivery(t, f.Context(), original, old)

			if endLifetime == "connection_close" {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				closeSubscription(t, original)
			}
			reopened := f.Subscribe(other, inbox)

			assertNoDelivery(t, f.Context(), reopened)
			if err := outstanding.Ack(f.Context()); !errors.Is(err, comms.ErrClosed) {
				t.Fatalf("expired delivery settlement: %v", err)
			}
			closeSubscription(t, original)
			if duplicate, err := other.Subscribe(f.Context(), inbox); duplicate != nil || !errors.Is(err, comms.ErrInboxInUse) {
				t.Fatalf("old close released the new owner: %v, %v", duplicate, err)
			}
			fresh := fixtures.Message(t)
			f.Publish(other, inbox.Topic, fresh)
			ack(t, f.Context(), nextDelivery(t, f.Context(), reopened, fresh))
		})
	}
}

func TestRabbitMQDuplicateIDsHaveIndependentIdempotentSettlements(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus := f.Bus(2)
	inbox := f.Inbox(comms.DurableShared)
	sub := f.Subscribe(bus, inbox)
	duplicate := fixtures.Message(t)
	f.Publish(bus, inbox.Topic, duplicate)
	f.Publish(bus, inbox.Topic, duplicate)
	first, second := nextDelivery(t, f.Context(), sub, duplicate), nextDelivery(t, f.Context(), sub, duplicate)

	ack(t, f.Context(), first)
	ack(t, f.Context(), first)
	if err := first.Reject(f.Context()); !errors.Is(err, comms.ErrDeliverySettled) {
		t.Fatalf("reject after ack: %v", err)
	}
	if err := second.Reject(f.Context()); err != nil {
		t.Fatalf("reject another delivery with the same message ID: %v", err)
	}
	if err := second.Reject(f.Context()); err != nil {
		t.Fatalf("repeated reject: %v", err)
	}
	if err := second.Ack(f.Context()); !errors.Is(err, comms.ErrDeliverySettled) {
		t.Fatalf("ack after reject: %v", err)
	}
	closeSubscription(t, sub)
	ack(t, f.Context(), first)
	if err := second.Reject(f.Context()); err != nil {
		t.Fatalf("committed reject after close: %v", err)
	}
	assertNoDelivery(t, f.Context(), f.Subscribe(bus, inbox))
	failures := f.Failures()
	if len(failures) != 1 || failures[0].MessageId != duplicate.ID {
		t.Fatalf("expected exactly one quarantined rejection: %+v", failures)
	}
}

func TestRabbitMQPrefetchWaitsForSettlementAndReceiveCancellationIsLocal(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus := f.Bus(1)
	inbox := f.Inbox(comms.TransientExclusive)
	sub := f.Subscribe(bus, inbox)
	one, two := fixtures.Message(t), fixtures.Message(t)
	f.Publish(bus, inbox.Topic, one)
	f.Publish(bus, inbox.Topic, two)
	first := nextDelivery(t, f.Context(), sub, one)

	assertNoDelivery(t, f.Context(), sub)
	canceled, cancel := context.WithCancel(f.Context())
	cancel()
	if err := first.Ack(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled settlement: %v", err)
	}
	ack(t, f.Context(), first)

	ack(t, f.Context(), nextDelivery(t, f.Context(), sub, two))
}

func TestRabbitMQConcurrentPublicationsCorrelateReturnsAndConfirmations(t *testing.T) {
	f := fixtures.NewBroker(t)
	bus := f.Bus(0)
	inbox := f.Inbox(comms.TransientExclusive)
	sub := f.Subscribe(bus, inbox)
	unboundTopic := f.Inbox(comms.TransientExclusive).Topic
	type result struct {
		routed bool
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 16)
	pending := make(map[string]struct{}, 8)
	for range 8 {
		publication := fixtures.Message(t)
		pending[publication.ID] = struct{}{}
		for _, topic := range []string{inbox.Topic, unboundTopic} {
			go func() {
				<-start
				results <- result{routed: topic == inbox.Topic, err: bus.Publish(f.Context(), topic, publication)}
			}()
		}
	}

	close(start)

	for range 16 {
		select {
		case got := <-results:
			if got.routed && got.err != nil {
				t.Fatalf("routed publication received another publication's return: %v", got.err)
			}
			if !got.routed && !errors.Is(got.err, rabbitcomms.ErrUnroutable) {
				t.Fatalf("unbound publication received another publication's confirmation: %v", got.err)
			}
		case <-f.Context().Done():
			t.Fatal("concurrent publications did not complete")
		}
	}
	for range 8 {
		delivery, err := sub.Next(f.Context())
		if err != nil {
			t.Fatal(err)
		}
		got := delivery.Message().ID
		if _, exists := pending[got]; !exists {
			t.Fatalf("received duplicate or unexpected publication %q", got)
		}
		delete(pending, got)
		ack(t, f.Context(), delivery)
	}
	assertNoDelivery(t, f.Context(), sub)
}
