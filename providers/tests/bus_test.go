package providers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"commander/framework/comms"
	"commander/framework/comms/messages"
	fscomms "commander/providers/fs/comms"
)

func TestBusAcknowledgesOnlyCompletedDeliveriesAndRetainsFailures(t *testing.T) {
	root := t.TempDir()
	open := func() *fscomms.FsBus {
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
	publisher, receiver := open(), open()
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
	message, err := messages.NewMessage(messages.AgentHeartbeat, "review-agent-00000001", nil)
	if err != nil {
		t.Fatal(err)
	}
	files := func() int {
		entries, err := filepath.Glob(filepath.Join(root, "*", "[0-9]*.json"))
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}

	if err := publisher.Publish(ctx, "results", message); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []comms.Subscription{first, second} {
		got, err := sub.Next(ctx)
		if err != nil || got.ID != message.ID {
			t.Fatalf("cross-instance delivery: %+v, %v", got, err)
		}
	}
	if err := first.Ack(ctx, message.ID); err != nil {
		t.Fatal(err)
	}
	if files() != 1 {
		t.Fatal("message removed before every recipient acknowledged")
	}
	if err := second.Ack(ctx, message.ID); err != nil {
		t.Fatal(err)
	}
	if err := second.Ack(ctx, message.ID); err != nil {
		t.Fatalf("ack retry must be harmless: %v", err)
	}
	if files() != 0 {
		t.Fatal("completed delivery was not removed")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	failure, err := messages.NewMessage(messages.AgentHeartbeat, message.AgentID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(ctx, "results", failure); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("database save failed")
	consumer := comms.Consumer{Subscription: second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	err = consumer.Consume(ctx, func(context.Context, messages.Message) error { return rejected })
	if closeErr := second.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	if !errors.Is(err, rejected) || files() != 1 {
		t.Fatalf("failed delivery was acknowledged or removed: %v, files=%d", err, files())
	}
}
