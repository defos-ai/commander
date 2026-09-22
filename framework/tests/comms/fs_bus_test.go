package comms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
	fscomms "commander/providers/fs/comms"
)

func openBus(t *testing.T, root string) comms.Bus {
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

func subscribe(t *testing.T, bus comms.Bus, topic string) comms.Subscription {
	t.Helper()
	sub, err := bus.Subscribe(t.Context(), topic)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	return sub
}

func message(t *testing.T, value string) messages.Message {
	t.Helper()
	m, err := messages.NewMessage(messages.RunAction, "fixture-agent-a5452d64", map[string]string{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestIndependentBusesDeliverOrderedMessagesToEverySubscriber(t *testing.T) {
	// Arrange
	root := t.TempDir()
	publisher, receiver := openBus(t, root), openBus(t, root)
	first, second := subscribe(t, publisher, "work"), subscribe(t, receiver, "work")
	want := []messages.Message{message(t, "first"), message(t, "second")}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	// Act
	for _, m := range want {
		if err := publisher.Publish(ctx, "work", m); err != nil {
			t.Fatal(err)
		}
	}
	var deliveries [2][]messages.Message
	for index, sub := range []comms.Subscription{first, second} {
		for range want {
			m, err := sub.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			deliveries[index] = append(deliveries[index], m)
			if err := sub.Ack(ctx, m.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Acknowledgement retries remain harmless after the final subscriber finishes.
	retryErr := first.Ack(ctx, want[0].ID)
	unknownErr := second.Ack(ctx, "unknown-message")

	// Assert
	for index, got := range deliveries {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("subscriber %d: got %+v, want %+v", index, got, want)
		}
	}
	if retryErr != nil || !errors.Is(unknownErr, comms.ErrNotDelivered) {
		t.Fatalf("acknowledgements: retry=%v unknown=%v", retryErr, unknownErr)
	}
}

func TestSubscriptionsReceiveOnlyNewMessagesOnTheirExactTopic(t *testing.T) {
	// Arrange
	root := t.TempDir()
	bus := openBus(t, root)
	if err := bus.Publish(t.Context(), "work", message(t, "before subscription")); err != nil {
		t.Fatal(err)
	}
	sub := subscribe(t, openBus(t, root), "work")
	want := message(t, "current work")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	// Act
	if err := bus.Publish(ctx, "work.child", message(t, "other topic")); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, "work", want); err != nil {
		t.Fatal(err)
	}
	got, err := sub.Next(ctx)

	// Assert
	if err != nil || got.ID != want.ID {
		t.Fatalf("delivered stale or unrelated work: %+v, %v", got, err)
	}
}

func TestCancelledReceiveKeepsSubscriptionUsableUntilClosed(t *testing.T) {
	// Arrange
	bus := openBus(t, t.TempDir())
	sub := subscribe(t, bus, "work")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	ctx, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	want := message(t, "after cancellation")

	// Act
	_, cancelErr := sub.Next(cancelled)
	if err := bus.Publish(ctx, "work", want); err != nil {
		t.Fatal(err)
	}
	got, receiveErr := sub.Next(ctx)
	closeErr := sub.Close()
	_, closedErr := sub.Next(ctx)

	// Assert
	if !errors.Is(cancelErr, context.Canceled) || receiveErr != nil || got.ID != want.ID {
		t.Fatalf("receive cancellation: %v; subsequent delivery: %+v, %v", cancelErr, got, receiveErr)
	}
	if closeErr != nil || !errors.Is(closedErr, comms.ErrClosed) {
		t.Fatalf("subscription close: %v; subsequent receive: %v", closeErr, closedErr)
	}
}

func TestFsBusRejectsMessagesWithoutRecipientMetadata(t *testing.T) {
	for _, action := range []string{"receive", "acknowledge"} {
		t.Run(action, func(t *testing.T) {
			// Arrange: remove the recipient snapshot from an otherwise valid
			// publication, after delivery when exercising acknowledgement.
			root := t.TempDir()
			bus := openBus(t, root)
			sub := subscribe(t, bus, "work")
			want := message(t, "missing recipients")
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if err := bus.Publish(ctx, "work", want); err != nil {
				t.Fatal(err)
			}
			if action == "acknowledge" {
				got, err := sub.Next(ctx)
				if err != nil || got.ID != want.ID {
					t.Fatalf("initial delivery = %+v, %v", got, err)
				}
			}
			paths, err := filepath.Glob(filepath.Join(root, "*", "00000000000000000001.json"))
			if err != nil || len(paths) != 1 {
				t.Fatalf("publication paths = %v, %v", paths, err)
			}
			data, err := os.ReadFile(paths[0])
			if err != nil {
				t.Fatal(err)
			}
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(data, &records); err != nil || len(records) != 1 {
				t.Fatalf("stored publication = %s, %v", data, err)
			}
			delete(records[0], "pending")
			data, err = json.Marshal(records)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths[0], data, 0600); err != nil {
				t.Fatal(err)
			}

			// Act
			var operationErr error
			if action == "receive" {
				_, operationErr = sub.Next(ctx)
			} else {
				operationErr = sub.Ack(ctx, want.ID)
			}
			retained, readErr := os.ReadFile(paths[0])

			// Assert: incomplete recipient data cannot authorize delivery or
			// acknowledgement, and the rejected record remains unchanged.
			if operationErr == nil || ctx.Err() != nil {
				t.Fatalf("missing recipients were not rejected: operation=%v context=%v", operationErr, ctx.Err())
			}
			if readErr != nil || !bytes.Equal(retained, data) {
				t.Fatalf("rejected publication changed: got=%s error=%v", retained, readErr)
			}
		})
	}
}
