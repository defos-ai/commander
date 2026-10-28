package fixtures

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
)

func PendingBusDelivery(t *testing.T) (string, comms.Delivery, comms.Subscription) {
	t.Helper()
	root := t.TempDir()
	bus := OpenBus(t, root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	sub, err := bus.Subscribe(ctx, comms.Inbox{Topic: "results", Name: "receiver", Mode: comms.TransientExclusive})
	if err != nil {
		t.Fatal(err)
	}
	message := BusMessage(t)
	if err := bus.Publish(ctx, "results", message); err != nil {
		t.Fatal(err)
	}
	delivery, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if delivery == nil || delivery.Message().ID != message.ID {
		t.Fatalf("unexpected setup delivery: %+v", delivery)
	}
	return root, delivery, sub
}

type StoredBusMessage struct {
	Sequence uint64           `json:"sequence"`
	Message  messages.Message `json:"message"`
	Pending  []string         `json:"pending"`
	Rejected []string         `json:"rejected"`
	Path     string           `json:"-"`
}

func StoredBusMessages(t *testing.T, root string) []StoredBusMessage {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "*", "[0-9]*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var records []StoredBusMessage
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var entries []StoredBusMessage
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected one message record in %s: %+v", path, entries)
		}
		entries[0].Path = path
		records = append(records, entries[0])
	}
	return records
}
