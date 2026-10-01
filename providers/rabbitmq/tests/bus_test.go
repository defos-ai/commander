package rabbitmq_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"commander/framework/comms"
	rabbitcomms "commander/providers/rabbitmq/comms"
)

func TestRabbitMQFansOutToNamedInboxesOnExactTopics(t *testing.T) {
	f := newFixture(t)
	bus, otherBus := f.bus(1), f.bus(1)
	inbox := f.inbox(comms.TransientExclusive)
	inbox.Topic += ".*"
	otherName := inbox
	otherName.Name += "-other"
	otherTopic := inbox
	otherTopic.Topic = strings.TrimSuffix(inbox.Topic, "*") + "events"
	first := f.subscribe(bus, inbox)
	second := f.subscribe(otherBus, otherName)
	third := f.subscribe(otherBus, otherTopic)
	fanout, separate := message(t), message(t)

	f.publish(bus, inbox.Topic, fanout)
	f.publish(bus, otherTopic.Topic, separate)

	ack(t, f.ctx, f.next(first, fanout))
	ack(t, f.ctx, f.next(second, fanout))
	ack(t, f.ctx, f.next(third, separate))
	for _, sub := range []comms.Subscription{first, second, third} {
		f.empty(sub)
	}
}

func TestRabbitMQDurableInboxSharesWorkAcrossConsumers(t *testing.T) {
	f := newFixture(t)
	bus, otherBus := f.bus(1), f.bus(1)
	inbox := f.inbox(comms.DurableShared)
	first := f.subscribe(bus, inbox)
	second := f.subscribe(otherBus, inbox)
	one, two := message(t), message(t)

	f.publish(bus, inbox.Topic, one)
	f.publish(bus, inbox.Topic, two)

	// With prefetch one, neither consumer can reserve both publications.
	deliveries := make([]comms.Delivery, 0, 2)
	for _, sub := range []comms.Subscription{first, second} {
		delivery, err := sub.Next(f.ctx)
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
		ack(t, f.ctx, delivery)
	}
	f.empty(first)
	f.empty(second)
}

func TestRabbitMQDurableInboxRedeliversAndRetainsWorkDuringDowntime(t *testing.T) {
	f := newFixture(t)
	owner, publisher := f.bus(1), f.bus(1)
	inbox := f.inbox(comms.DurableShared)
	first := f.subscribe(owner, inbox)
	unsettled := message(t)
	f.publish(publisher, inbox.Topic, unsettled)
	oldDelivery := f.next(first, unsettled)

	closeSubscription(t, first)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := f.subscribe(publisher, inbox)

	ack(t, f.ctx, f.next(reopened, unsettled))
	if err := oldDelivery.Ack(f.ctx); !errors.Is(err, comms.ErrClosed) {
		t.Fatalf("old delivery settlement: got %v, want ErrClosed", err)
	}
	closeSubscription(t, reopened)
	duringDowntime := message(t)
	f.publish(publisher, inbox.Topic, duringDowntime)
	afterDowntime := f.subscribe(publisher, inbox)
	ack(t, f.ctx, f.next(afterDowntime, duringDowntime))
	f.empty(afterDowntime)
}

func TestRabbitMQTransientInboxHasOneOwnerAndNoReplay(t *testing.T) {
	for _, endLifetime := range []string{"subscription_close", "connection_close"} {
		t.Run(endLifetime, func(t *testing.T) {
			f := newFixture(t)
			owner, other := f.bus(1), f.bus(1)
			inbox := f.inbox(comms.TransientExclusive)
			original := f.subscribe(owner, inbox)
			for _, bus := range []comms.Bus{owner, other} {
				if duplicate, err := bus.Subscribe(f.ctx, inbox); duplicate != nil || !errors.Is(err, comms.ErrInboxInUse) {
					t.Fatalf("second owner: got %v, %v, want ErrInboxInUse", duplicate, err)
				}
			}
			old := message(t)
			f.publish(other, inbox.Topic, old)
			outstanding := f.next(original, old)

			if endLifetime == "connection_close" {
				if err := owner.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				closeSubscription(t, original)
			}
			reopened := f.subscribe(other, inbox)

			f.empty(reopened)
			if err := outstanding.Ack(f.ctx); !errors.Is(err, comms.ErrClosed) {
				t.Fatalf("expired delivery settlement: %v", err)
			}
			closeSubscription(t, original)
			if duplicate, err := other.Subscribe(f.ctx, inbox); duplicate != nil || !errors.Is(err, comms.ErrInboxInUse) {
				t.Fatalf("old close released the new owner: %v, %v", duplicate, err)
			}
			fresh := message(t)
			f.publish(other, inbox.Topic, fresh)
			ack(t, f.ctx, f.next(reopened, fresh))
		})
	}
}

func TestRabbitMQDuplicateIDsHaveIndependentIdempotentSettlements(t *testing.T) {
	f := newFixture(t)
	bus := f.bus(2)
	inbox := f.inbox(comms.DurableShared)
	sub := f.subscribe(bus, inbox)
	duplicate := message(t)
	f.publish(bus, inbox.Topic, duplicate)
	f.publish(bus, inbox.Topic, duplicate)
	first, second := f.next(sub, duplicate), f.next(sub, duplicate)

	ack(t, f.ctx, first)
	ack(t, f.ctx, first)
	if err := first.Reject(f.ctx); !errors.Is(err, comms.ErrDeliverySettled) {
		t.Fatalf("reject after ack: %v", err)
	}
	if err := second.Reject(f.ctx); err != nil {
		t.Fatalf("reject another delivery with the same message ID: %v", err)
	}
	if err := second.Reject(f.ctx); err != nil {
		t.Fatalf("repeated reject: %v", err)
	}
	if err := second.Ack(f.ctx); !errors.Is(err, comms.ErrDeliverySettled) {
		t.Fatalf("ack after reject: %v", err)
	}
	closeSubscription(t, sub)
	ack(t, f.ctx, first)
	if err := second.Reject(f.ctx); err != nil {
		t.Fatalf("committed reject after close: %v", err)
	}
	f.empty(f.subscribe(bus, inbox))
	failures := f.failures()
	if len(failures) != 1 || failures[0].MessageId != duplicate.ID {
		t.Fatalf("expected exactly one quarantined rejection: %+v", failures)
	}
}

func TestRabbitMQPrefetchWaitsForSettlementAndReceiveCancellationIsLocal(t *testing.T) {
	f := newFixture(t)
	bus := f.bus(1)
	inbox := f.inbox(comms.TransientExclusive)
	sub := f.subscribe(bus, inbox)
	one, two := message(t), message(t)
	f.publish(bus, inbox.Topic, one)
	f.publish(bus, inbox.Topic, two)
	first := f.next(sub, one)

	f.empty(sub)
	canceled, cancel := context.WithCancel(f.ctx)
	cancel()
	if err := first.Ack(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled settlement: %v", err)
	}
	ack(t, f.ctx, first)

	ack(t, f.ctx, f.next(sub, two))
}

func TestRabbitMQConcurrentPublicationsCorrelateReturnsAndConfirmations(t *testing.T) {
	f := newFixture(t)
	bus := f.bus(0)
	inbox := f.inbox(comms.TransientExclusive)
	sub := f.subscribe(bus, inbox)
	unboundTopic := f.inbox(comms.TransientExclusive).Topic
	type result struct {
		routed bool
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 16)
	pending := make(map[string]struct{}, 8)
	for range 8 {
		publication := message(t)
		pending[publication.ID] = struct{}{}
		for _, topic := range []string{inbox.Topic, unboundTopic} {
			go func() {
				<-start
				results <- result{routed: topic == inbox.Topic, err: bus.Publish(f.ctx, topic, publication)}
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
		case <-f.ctx.Done():
			t.Fatal("concurrent publications did not complete")
		}
	}
	for range 8 {
		delivery, err := sub.Next(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := delivery.Message().ID
		if _, exists := pending[got]; !exists {
			t.Fatalf("received duplicate or unexpected publication %q", got)
		}
		delete(pending, got)
		ack(t, f.ctx, delivery)
	}
	f.empty(sub)
}
