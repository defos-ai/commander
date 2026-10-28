package providers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
	"github.com/defos-ai/commander/providers/fs/tests/fixtures"
)

func TestBusAcknowledgesOnlyCompletedDeliveriesAndRetainsFailures(t *testing.T) {
	root := t.TempDir()
	publisher, receiver := fixtures.OpenBus(t, root), fixtures.OpenBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := publisher.Subscribe(ctx, comms.Inbox{Topic: "results", Name: "first", Mode: comms.TransientExclusive})
	if err != nil {
		t.Fatal(err)
	}
	second, err := receiver.Subscribe(ctx, comms.Inbox{Topic: "results", Name: "second", Mode: comms.TransientExclusive})
	if err != nil {
		t.Fatal(err)
	}
	message := fixtures.BusMessage(t)
	if err := publisher.Publish(ctx, "results", message); err != nil {
		t.Fatal(err)
	}
	firstDelivery := nextBusDelivery(t, ctx, first, message)
	secondDelivery := nextBusDelivery(t, ctx, second, message)
	if err := firstDelivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.StoredBusMessages(t, root)) != 1 {
		t.Fatal("message removed before every recipient acknowledged")
	}
	if err := secondDelivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if err := secondDelivery.Ack(ctx); err != nil {
		t.Fatalf("ack retry must be harmless: %v", err)
	}
	if len(fixtures.StoredBusMessages(t, root)) != 0 {
		t.Fatal("completed delivery was not removed")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	failure := fixtures.BusMessage(t)
	if err := publisher.Publish(ctx, "results", failure); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("database save failed")
	consumer := comms.Consumer{Subscription: second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err = consumer.Consume(ctx, func(context.Context, messages.Message) error { return rejected })
	if closeErr := second.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	records := fixtures.StoredBusMessages(t, root)
	if !errors.Is(err, rejected) || len(records) != 1 || records[0].Message.ID != failure.ID {
		t.Fatalf("failed delivery was acknowledged or removed: %v, records=%+v", err, records)
	}
}

func TestBusDuplicateMessageIDsSettleIndependently(t *testing.T) {
	root := t.TempDir()
	bus := fixtures.OpenBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := bus.Subscribe(ctx, comms.Inbox{Topic: "results", Name: "receiver", Mode: comms.TransientExclusive})
	if err != nil {
		t.Fatal(err)
	}
	message := fixtures.BusMessage(t)
	for range 2 {
		if err := bus.Publish(ctx, "results", message); err != nil {
			t.Fatal(err)
		}
	}
	first := nextBusDelivery(t, ctx, sub, message)
	second := nextBusDelivery(t, ctx, sub, message)
	before := fixtures.StoredBusMessages(t, root)
	if len(before) != 2 || len(before[0].Pending) != 1 {
		t.Fatalf("expected two independent pending publications: %+v", before)
	}
	subscriber := before[0].Pending[0]
	if err := first.Reject(ctx); err != nil {
		t.Fatal(err)
	}
	records := fixtures.StoredBusMessages(t, root)
	if len(records) != 2 || len(records[0].Pending) != 0 || !slices.Equal(records[0].Rejected, []string{subscriber}) || !reflect.DeepEqual(records[1], before[1]) {
		t.Fatalf("reject changed another publication or lost evidence: %+v", records)
	}
	if err := second.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	records = fixtures.StoredBusMessages(t, root)
	if len(records) != 1 || records[0].Sequence != before[0].Sequence || len(records[0].Pending) != 0 || !slices.Equal(records[0].Rejected, []string{subscriber}) {
		t.Fatalf("ack of duplicate ID removed rejected publication: %+v", records)
	}
}

func TestBusSubscribersSettleTheSamePublicationIndependently(t *testing.T) {
	root := t.TempDir()
	publisher, receiver := fixtures.OpenBus(t, root), fixtures.OpenBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := publisher.Subscribe(ctx, comms.Inbox{Topic: "results", Name: "first", Mode: comms.TransientExclusive})
	if err != nil {
		t.Fatal(err)
	}
	second, err := receiver.Subscribe(ctx, comms.Inbox{Topic: "results", Name: "second", Mode: comms.TransientExclusive})
	if err != nil {
		t.Fatal(err)
	}
	message := fixtures.BusMessage(t)
	if err := publisher.Publish(ctx, "results", message); err != nil {
		t.Fatal(err)
	}
	firstDelivery := nextBusDelivery(t, ctx, first, message)
	secondDelivery := nextBusDelivery(t, ctx, second, message)
	before := fixtures.StoredBusMessages(t, root)
	if len(before) != 1 || len(before[0].Pending) != 2 {
		t.Fatalf("expected two pending subscribers: %+v", before)
	}
	// A subscriber may still be reading the old snapshot while another settles.
	directory, err := os.OpenRoot(filepath.Dir(before[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	reader, err := directory.Open(filepath.Base(before[0].Path))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := firstDelivery.Reject(ctx); err != nil {
		t.Fatal(err)
	}
	var original []fixtures.StoredBusMessage
	if err := json.NewDecoder(reader).Decode(&original); err != nil {
		t.Fatal(err)
	}
	if len(original) != 1 || !slices.Equal(original[0].Pending, before[0].Pending) || len(original[0].Rejected) != 0 {
		t.Fatal("settlement changed the snapshot held by another reader")
	}
	records := fixtures.StoredBusMessages(t, root)
	if len(records) != 1 || len(records[0].Pending) != 1 || len(records[0].Rejected) != 1 {
		t.Fatalf("reject did not settle exactly one subscriber: %+v", records)
	}
	rejectedSubscriber := records[0].Rejected[0]
	pendingSubscriber := records[0].Pending[0]
	if rejectedSubscriber == pendingSubscriber || !slices.Contains(before[0].Pending, rejectedSubscriber) || !slices.Contains(before[0].Pending, pendingSubscriber) {
		t.Fatalf("settlement changed subscriber identities: %+v", records[0])
	}
	if err := secondDelivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	records = fixtures.StoredBusMessages(t, root)
	if len(records) != 1 || len(records[0].Pending) != 0 || !slices.Equal(records[0].Rejected, []string{rejectedSubscriber}) || records[0].Message.ID != message.ID {
		t.Fatalf("ack or close lost another subscriber's rejection evidence: %+v", records)
	}
}

func TestBusDeliverySettlementRetriesAndConflicts(t *testing.T) {
	for _, reject := range []bool{false, true} {
		name := "ack"
		if reject {
			name = "reject"
		}
		t.Run(name, func(t *testing.T) {
			root, delivery, sub := fixtures.PendingBusDelivery(t)
			settle, opposite := delivery.Ack, delivery.Reject
			if reject {
				settle, opposite = opposite, settle
			}
			if err := settle(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertBusSettlement(t, root, reject)
			before := fixtures.StoredBusMessages(t, root)
			if err := settle(t.Context()); err != nil {
				t.Fatalf("same settlement retry: %v", err)
			}
			if err := opposite(t.Context()); !errors.Is(err, comms.ErrDeliverySettled) {
				t.Fatalf("opposite settlement returned %v, want ErrDeliverySettled", err)
			}
			if err := sub.Close(); err != nil {
				t.Fatal(err)
			}
			if err := settle(t.Context()); err != nil {
				t.Fatalf("same settlement retry after subscription close: %v", err)
			}
			if after := fixtures.StoredBusMessages(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("settlement retry changed evidence: before=%+v, after=%+v", before, after)
			}
		})
	}
}

func TestBusConcurrentSettlementCommitsOneOutcome(t *testing.T) {
	root, delivery, sub := fixtures.PendingBusDelivery(t)
	type outcome struct {
		rejected bool
		err      error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	go func() {
		<-start
		results <- outcome{err: delivery.Ack(t.Context())}
	}()
	go func() {
		<-start
		results <- outcome{rejected: true, err: delivery.Reject(t.Context())}
	}()
	close(start)
	completed := []outcome{<-results, <-results}
	successes := 0
	rejected := false
	for _, result := range completed {
		if result.err == nil {
			successes++
			rejected = result.rejected
		} else if !errors.Is(result.err, comms.ErrDeliverySettled) {
			t.Fatalf("concurrent settlement returned an unexpected error: %v", result.err)
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one settlement must commit: %+v", completed)
	}
	assertBusSettlement(t, root, rejected)
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	retry, opposite := delivery.Ack, delivery.Reject
	if rejected {
		retry, opposite = opposite, retry
	}
	if err := retry(t.Context()); err != nil {
		t.Fatalf("committed settlement retry after close: %v", err)
	}
	if err := opposite(t.Context()); !errors.Is(err, comms.ErrDeliverySettled) {
		t.Fatalf("opposite settlement after close returned %v, want ErrDeliverySettled", err)
	}
	assertBusSettlement(t, root, rejected)
}

func TestBusCanceledDeliverySettlementCanRetry(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		reject        bool
		retryOpposite bool
	}{
		{name: "ack_then_ack"},
		{name: "ack_then_reject", retryOpposite: true},
		{name: "reject_then_reject", reject: true},
		{name: "reject_then_ack", reject: true, retryOpposite: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reject := scenario.reject
			root, delivery, _ := fixtures.PendingBusDelivery(t)
			settle := delivery.Ack
			if reject {
				settle = delivery.Reject
			}
			before := fixtures.StoredBusMessages(t, root)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := settle(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled settlement returned %v", err)
			}
			if after := fixtures.StoredBusMessages(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("canceled settlement changed evidence: before=%+v, after=%+v", before, after)
			}
			if scenario.retryOpposite {
				reject = !reject
				settle = delivery.Ack
				if reject {
					settle = delivery.Reject
				}
			}
			if err := settle(t.Context()); err != nil {
				t.Fatalf("retry after cancellation: %v", err)
			}
			assertBusSettlement(t, root, reject)
		})
	}
}

func TestBusDeliverySettlementFailureBeforeCommitCanRetry(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		reject        bool
		retryOpposite bool
	}{
		{name: "ack_then_ack"},
		{name: "ack_then_reject", retryOpposite: true},
		{name: "reject_then_reject", reject: true},
		{name: "reject_then_ack", reject: true, retryOpposite: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reject := scenario.reject
			root, delivery, _ := fixtures.PendingBusDelivery(t)
			settle := delivery.Ack
			if reject {
				settle = delivery.Reject
			}
			record := fixtures.StoredBusMessages(t, root)[0]
			original, err := os.ReadFile(record.Path)
			if err != nil {
				t.Fatal(err)
			}
			broken := []byte("{invalid JSON")
			if err := os.WriteFile(record.Path, broken, 0600); err != nil {
				t.Fatal(err)
			}
			if err := settle(t.Context()); err == nil {
				t.Fatal("settlement unexpectedly accepted a malformed record")
			}
			if current, err := os.ReadFile(record.Path); err != nil || !slices.Equal(current, broken) {
				t.Fatalf("failed settlement changed the stored record: %q, %v", current, err)
			}
			if err := os.WriteFile(record.Path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if scenario.retryOpposite {
				reject = !reject
				settle = delivery.Ack
				if reject {
					settle = delivery.Reject
				}
			}
			if err := settle(t.Context()); err != nil {
				t.Fatalf("retry after repairing storage: %v", err)
			}
			assertBusSettlement(t, root, reject)
		})
	}
}

func TestBusDeliveryMessageReturnsAnIndependentSnapshot(t *testing.T) {
	root, delivery, _ := fixtures.PendingBusDelivery(t)
	original := delivery.Message()
	original.Payload = slices.Clone(original.Payload)
	changed := delivery.Message()
	changed.ID = "changed"
	changed.Payload[0] = '!'
	if got := delivery.Message(); !reflect.DeepEqual(got, original) {
		t.Fatalf("caller mutation changed the delivery: got=%+v, want=%+v", got, original)
	}
	if err := delivery.Ack(t.Context()); err != nil {
		t.Fatalf("caller mutation changed settlement identity: %v", err)
	}
	assertBusSettlement(t, root, false)
}

func TestBusInboxIsExclusiveAcrossInstancesAndScopedByTopic(t *testing.T) {
	root := t.TempDir()
	firstBus, secondBus := fixtures.OpenBus(t, root), fixtures.OpenBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	inbox := comms.Inbox{Topic: "results", Name: "worker", Mode: comms.TransientExclusive}
	first, err := firstBus.Subscribe(ctx, inbox)
	if err != nil {
		t.Fatal(err)
	}
	for _, bus := range []*fscomms.FsBus{firstBus, secondBus} {
		if duplicate, err := bus.Subscribe(ctx, inbox); !errors.Is(err, comms.ErrInboxInUse) || duplicate != nil {
			t.Fatalf("duplicate inbox returned %v, %v; want ErrInboxInUse", duplicate, err)
		}
	}
	otherInbox := inbox
	otherInbox.Topic = "other-results"
	other, err := secondBus.Subscribe(ctx, otherInbox)
	if err != nil {
		t.Fatalf("same inbox name on another topic: %v", err)
	}
	firstMessage, otherMessage := fixtures.BusMessage(t), fixtures.BusMessage(t)
	if err := secondBus.Publish(ctx, inbox.Topic, firstMessage); err != nil {
		t.Fatal(err)
	}
	if err := firstBus.Publish(ctx, otherInbox.Topic, otherMessage); err != nil {
		t.Fatal(err)
	}
	if err := nextBusDelivery(t, ctx, first, firstMessage).Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if err := nextBusDelivery(t, ctx, other, otherMessage).Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if records := fixtures.StoredBusMessages(t, root); len(records) != 0 {
		t.Fatalf("topic-scoped inboxes received another topic's publication: %+v", records)
	}
}

func TestBusReopenedInboxStartsAtTailAndRetainsOldDelivery(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "closed"
		if expired {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			firstBus, secondBus := fixtures.OpenBus(t, root), fixtures.OpenBus(t, root)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			inbox := comms.Inbox{Topic: "results", Name: "worker", Mode: comms.TransientExclusive}
			old, err := firstBus.Subscribe(ctx, inbox)
			if err != nil {
				t.Fatal(err)
			}
			oldMessage := fixtures.BusMessage(t)
			if err := firstBus.Publish(ctx, inbox.Topic, oldMessage); err != nil {
				t.Fatal(err)
			}
			oldDelivery := nextBusDelivery(t, ctx, old, oldMessage)
			before := fixtures.StoredBusMessages(t, root)
			if len(before) != 1 || len(before[0].Pending) != 1 {
				t.Fatalf("expected one outstanding delivery: %+v", before)
			}
			if expired {
				fixtures.ExpireBusSubscriber(t, root, before[0].Pending[0])
			} else {
				if err := old.Close(); err != nil {
					t.Fatal(err)
				}
				if err := firstBus.Publish(ctx, inbox.Topic, fixtures.BusMessage(t)); err != nil {
					t.Fatal(err)
				}
			}
			current, err := secondBus.Subscribe(ctx, inbox)
			if err != nil {
				t.Fatalf("reopen inbox after owner %s: %v", name, err)
			}
			for _, settle := range []func(context.Context) error{oldDelivery.Ack, oldDelivery.Reject} {
				if err := settle(ctx); !errors.Is(err, comms.ErrClosed) {
					t.Fatalf("old outstanding delivery settlement returned %v, want ErrClosed", err)
				}
			}
			if delivery, err := old.Next(ctx); !errors.Is(err, comms.ErrClosed) || delivery != nil {
				t.Fatalf("old subscription returned %v, %v; want ErrClosed", delivery, err)
			}
			if err := old.Close(); err != nil {
				t.Fatalf("stale subscription close: %v", err)
			}
			if duplicate, err := firstBus.Subscribe(ctx, inbox); !errors.Is(err, comms.ErrInboxInUse) || duplicate != nil {
				t.Fatalf("stale close released successor's ownership: %v, %v", duplicate, err)
			}
			fresh := fixtures.BusMessage(t)
			if err := secondBus.Publish(ctx, inbox.Topic, fresh); err != nil {
				t.Fatal(err)
			}
			if err := nextBusDelivery(t, ctx, current, fresh).Ack(ctx); err != nil {
				t.Fatal(err)
			}
			if after := fixtures.StoredBusMessages(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("reopen or new settlement changed old pending evidence: before=%+v, after=%+v", before, after)
			}
		})
	}
}

func TestBusInvalidAndUnsupportedInboxesCreateNoPaths(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		inbox comms.Inbox
		want  error
	}{
		{name: "durable", inbox: comms.Inbox{Topic: "results", Name: "worker", Mode: comms.DurableShared}, want: comms.ErrUnsupportedInbox},
		{name: "zero", inbox: comms.Inbox{}},
		{name: "blank_topic", inbox: comms.Inbox{Topic: " ", Name: "worker", Mode: comms.TransientExclusive}},
		{name: "blank_name", inbox: comms.Inbox{Topic: "results", Name: " ", Mode: comms.TransientExclusive}},
		{name: "padded_topic", inbox: comms.Inbox{Topic: " results", Name: "worker", Mode: comms.TransientExclusive}},
		{name: "padded_name", inbox: comms.Inbox{Topic: "results", Name: "worker ", Mode: comms.TransientExclusive}},
		{name: "invalid_topic", inbox: comms.Inbox{Topic: "\xff", Name: "worker", Mode: comms.TransientExclusive}},
		{name: "invalid_name", inbox: comms.Inbox{Topic: "results", Name: "\xff", Mode: comms.TransientExclusive}},
		{name: "missing_mode", inbox: comms.Inbox{Topic: "results", Name: "worker"}},
		{name: "unknown_mode", inbox: comms.Inbox{Topic: "results", Name: "worker", Mode: "unknown"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			parent := t.TempDir()
			bus := fixtures.OpenBus(t, filepath.Join(parent, "bus"))
			if scenario.want != nil {
				if err := scenario.inbox.Validate(); err != nil {
					t.Fatalf("unsupported inbox must otherwise be valid: %v", err)
				}
			}
			sub, err := bus.Subscribe(t.Context(), scenario.inbox)
			if err == nil || sub != nil || (scenario.want != nil && !errors.Is(err, scenario.want)) {
				t.Fatalf("invalid or unsupported inbox returned %v, %v; want %v", sub, err, scenario.want)
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("rejected inbox created filesystem paths: %v", entries)
			}
		})
	}
}

func TestBusInvalidPublishTopicsCreateNoPaths(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		topic string
	}{
		{name: "empty"},
		{name: "blank", topic: " "},
		{name: "leading_whitespace", topic: " results"},
		{name: "trailing_whitespace", topic: "results "},
		{name: "invalid_utf8", topic: "\xff"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			parent := t.TempDir()
			bus := fixtures.OpenBus(t, filepath.Join(parent, "bus"))
			if err := bus.Publish(t.Context(), scenario.topic, fixtures.BusMessage(t)); err == nil {
				t.Fatal("invalid publication topic was accepted")
			}
			entries, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("invalid publication topic created filesystem paths: %v", entries)
			}
		})
	}
}

func nextBusDelivery(t *testing.T, ctx context.Context, sub comms.Subscription, message messages.Message) comms.Delivery {
	t.Helper()
	delivery, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if delivery == nil || delivery.Message().ID != message.ID {
		t.Fatalf("unexpected delivery: %+v", delivery)
	}
	return delivery
}

func assertBusSettlement(t *testing.T, root string, rejected bool) {
	t.Helper()
	records := fixtures.StoredBusMessages(t, root)
	if rejected {
		if len(records) != 1 || len(records[0].Pending) != 0 || len(records[0].Rejected) != 1 {
			t.Fatalf("rejection must retain evidence and remove pending recipient: %+v", records)
		}
	} else if len(records) != 0 {
		t.Fatalf("acknowledgement must remove fully completed message: %+v", records)
	}
}
