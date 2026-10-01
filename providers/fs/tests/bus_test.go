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

	"commander/framework/comms"
	"commander/framework/comms/messages"
	fscomms "commander/providers/fs/comms"
)

func TestBusAcknowledgesOnlyCompletedDeliveriesAndRetainsFailures(t *testing.T) {
	root := t.TempDir()
	publisher, receiver := openTestFsBus(t, root), openTestFsBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := publisher.Subscribe(ctx, "results")
	if err != nil {
		t.Fatal(err)
	}
	second, err := receiver.Subscribe(ctx, "results")
	if err != nil {
		t.Fatal(err)
	}
	message := testBusMessage(t)
	if err := publisher.Publish(ctx, "results", message); err != nil {
		t.Fatal(err)
	}
	firstDelivery := nextBusDelivery(t, ctx, first, message)
	secondDelivery := nextBusDelivery(t, ctx, second, message)
	if err := firstDelivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if len(storedBusMessages(t, root)) != 1 {
		t.Fatal("message removed before every recipient acknowledged")
	}
	if err := secondDelivery.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	if err := secondDelivery.Ack(ctx); err != nil {
		t.Fatalf("ack retry must be harmless: %v", err)
	}
	if len(storedBusMessages(t, root)) != 0 {
		t.Fatal("completed delivery was not removed")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	failure := testBusMessage(t)
	if err := publisher.Publish(ctx, "results", failure); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("database save failed")
	consumer := comms.Consumer{Subscription: second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err = consumer.Consume(ctx, func(context.Context, messages.Message) error { return rejected })
	if closeErr := second.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	records := storedBusMessages(t, root)
	if !errors.Is(err, rejected) || len(records) != 1 || records[0].Message.ID != failure.ID {
		t.Fatalf("failed delivery was acknowledged or removed: %v, records=%+v", err, records)
	}
}

func TestBusDuplicateMessageIDsSettleIndependently(t *testing.T) {
	root := t.TempDir()
	bus := openTestFsBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := bus.Subscribe(ctx, "results")
	if err != nil {
		t.Fatal(err)
	}
	message := testBusMessage(t)
	for range 2 {
		if err := bus.Publish(ctx, "results", message); err != nil {
			t.Fatal(err)
		}
	}
	first := nextBusDelivery(t, ctx, sub, message)
	second := nextBusDelivery(t, ctx, sub, message)
	before := storedBusMessages(t, root)
	if len(before) != 2 || len(before[0].Pending) != 1 {
		t.Fatalf("expected two independent pending publications: %+v", before)
	}
	subscriber := before[0].Pending[0]
	if err := first.Reject(ctx); err != nil {
		t.Fatal(err)
	}
	records := storedBusMessages(t, root)
	if len(records) != 2 || len(records[0].Pending) != 0 || !slices.Equal(records[0].Rejected, []string{subscriber}) || !reflect.DeepEqual(records[1], before[1]) {
		t.Fatalf("reject changed another publication or lost evidence: %+v", records)
	}
	if err := second.Ack(ctx); err != nil {
		t.Fatal(err)
	}
	records = storedBusMessages(t, root)
	if len(records) != 1 || records[0].Sequence != before[0].Sequence || len(records[0].Pending) != 0 || !slices.Equal(records[0].Rejected, []string{subscriber}) {
		t.Fatalf("ack of duplicate ID removed rejected publication: %+v", records)
	}
}

func TestBusSubscribersSettleTheSamePublicationIndependently(t *testing.T) {
	root := t.TempDir()
	publisher, receiver := openTestFsBus(t, root), openTestFsBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := publisher.Subscribe(ctx, "results")
	if err != nil {
		t.Fatal(err)
	}
	second, err := receiver.Subscribe(ctx, "results")
	if err != nil {
		t.Fatal(err)
	}
	message := testBusMessage(t)
	if err := publisher.Publish(ctx, "results", message); err != nil {
		t.Fatal(err)
	}
	firstDelivery := nextBusDelivery(t, ctx, first, message)
	secondDelivery := nextBusDelivery(t, ctx, second, message)
	before := storedBusMessages(t, root)
	if len(before) != 1 || len(before[0].Pending) != 2 {
		t.Fatalf("expected two pending subscribers: %+v", before)
	}
	if err := firstDelivery.Reject(ctx); err != nil {
		t.Fatal(err)
	}
	records := storedBusMessages(t, root)
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
	records = storedBusMessages(t, root)
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
			root, delivery, sub := pendingBusDelivery(t)
			settle, opposite := delivery.Ack, delivery.Reject
			if reject {
				settle, opposite = opposite, settle
			}
			if err := settle(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertBusSettlement(t, root, reject)
			before := storedBusMessages(t, root)
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
			if after := storedBusMessages(t, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("settlement retry changed evidence: before=%+v, after=%+v", before, after)
			}
		})
	}
}

func TestBusConcurrentSettlementCommitsOneOutcome(t *testing.T) {
	root, delivery, sub := pendingBusDelivery(t)
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
			root, delivery, _ := pendingBusDelivery(t)
			settle := delivery.Ack
			if reject {
				settle = delivery.Reject
			}
			before := storedBusMessages(t, root)
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := settle(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled settlement returned %v", err)
			}
			if after := storedBusMessages(t, root); !reflect.DeepEqual(before, after) {
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
			root, delivery, _ := pendingBusDelivery(t)
			settle := delivery.Ack
			if reject {
				settle = delivery.Reject
			}
			record := storedBusMessages(t, root)[0]
			original, err := os.ReadFile(record.path)
			if err != nil {
				t.Fatal(err)
			}
			broken := []byte("{invalid JSON")
			if err := os.WriteFile(record.path, broken, 0600); err != nil {
				t.Fatal(err)
			}
			if err := settle(t.Context()); err == nil {
				t.Fatal("settlement unexpectedly accepted a malformed record")
			}
			if current, err := os.ReadFile(record.path); err != nil || !slices.Equal(current, broken) {
				t.Fatalf("failed settlement changed the stored record: %q, %v", current, err)
			}
			if err := os.WriteFile(record.path, original, 0600); err != nil {
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
	root, delivery, _ := pendingBusDelivery(t)
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

func openTestFsBus(t *testing.T, root string) *fscomms.FsBus {
	t.Helper()
	bus, err := fscomms.NewFsBus(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Error(err)
		}
	})
	return bus
}

func testBusMessage(t *testing.T) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(messages.AgentHeartbeat, "review-agent-00000001", nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
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

func pendingBusDelivery(t *testing.T) (string, comms.Delivery, comms.Subscription) {
	t.Helper()
	root := t.TempDir()
	bus := openTestFsBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := bus.Subscribe(ctx, "results")
	if err != nil {
		t.Fatal(err)
	}
	message := testBusMessage(t)
	if err := bus.Publish(ctx, "results", message); err != nil {
		t.Fatal(err)
	}
	return root, nextBusDelivery(t, ctx, sub, message), sub
}

type storedBusMessage struct {
	Sequence uint64           `json:"sequence"`
	Message  messages.Message `json:"message"`
	Pending  []string         `json:"pending"`
	Rejected []string         `json:"rejected"`
	path     string
}

func storedBusMessages(t *testing.T, root string) []storedBusMessage {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "*", "[0-9]*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []storedBusMessage
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var entries []storedBusMessage
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected one message record in %s: %+v", path, entries)
		}
		entries[0].path = path
		records = append(records, entries[0])
	}
	return records
}

func assertBusSettlement(t *testing.T, root string, rejected bool) {
	t.Helper()
	records := storedBusMessages(t, root)
	if rejected {
		if len(records) != 1 || len(records[0].Pending) != 0 || len(records[0].Rejected) != 1 {
			t.Fatalf("rejection must retain evidence and remove pending recipient: %+v", records)
		}
	} else if len(records) != 0 {
		t.Fatalf("acknowledgement must remove fully completed message: %+v", records)
	}
}
