package fixtures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	fscomms "github.com/defos-ai/commander/providers/fs/comms"
)

func ExpireBusSubscriber(t *testing.T, root, subscriber string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, "*", "state.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected one topic state: %v, %v", paths, err)
	}
	data, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil || len(records) != 1 {
		t.Fatalf("decode topic state: %s, %v", data, err)
	}
	var subscribers map[string]time.Time
	if err := json.Unmarshal(records[0]["subscribers"], &subscribers); err != nil {
		t.Fatal(err)
	}
	if _, exists := subscribers[subscriber]; !exists {
		t.Fatalf("subscriber %s missing from persisted lease state", subscriber)
	}
	subscribers[subscriber] = time.Now().UTC().Add(-time.Minute)
	records[0]["subscribers"], err = json.Marshal(subscribers)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths[0], data, 0600); err != nil {
		t.Fatal(err)
	}
}

func OpenBus(t *testing.T, root string) *fscomms.FsBus {
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

func BusMessage(t *testing.T) messages.Message {
	t.Helper()
	message, err := messages.NewMessage(messages.AgentHeartbeat, "review-agent-00000001", nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
