package providers_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/agents"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
)

func TestAgentFsStoreObservationOrdering(t *testing.T) {
	root := t.TempDir()
	first := openAgentFsStore(t, root)
	second := openAgentFsStore(t, root)
	observed := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	record := fsInventoryAgent(1, observed)
	if err := first.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	stored, err := second.Get(t.Context(), record.ID)
	if err != nil || !reflect.DeepEqual(stored, record) {
		t.Fatalf("source observation round trip = %#v, %v", stored, err)
	}
	path := filepath.Join(root, "agents", record.ID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stamp := range []time.Time{time.Time{}, observed.Add(-time.Nanosecond), observed} {
		stale := record.Clone()
		stale.ObservedAt, stale.Status = stamp, agents.AgentStopped
		stale.LastHeartbeat = observed.Add(time.Hour)
		if err := second.Upsert(t.Context(), stale); !errors.Is(err, agents.ErrStaleObservation) {
			t.Fatalf("stale observation %v: %v", stamp, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatalf("stale write changed persisted record: %s, %v", after, err)
		}
	}
	newer := record.Clone()
	newer.ObservedAt, newer.Status = observed.Add(time.Nanosecond), agents.AgentWorking
	if err := second.Upsert(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	if err := first.Upsert(t.Context(), record); !errors.Is(err, agents.ErrStaleObservation) {
		t.Fatalf("older store instance replaced newer observation: %v", err)
	}
	stored, err = first.Get(t.Context(), record.ID)
	if err != nil || !reflect.DeepEqual(stored, newer) {
		t.Fatalf("newer observation = %#v, %v", stored, err)
	}

	unordered := fsInventoryAgent(2, observed)
	unordered.ObservedAt = time.Time{}
	if err := first.Upsert(t.Context(), unordered); err != nil {
		t.Fatal(err)
	}
	unordered.Status = agents.AgentWorking
	if err := second.Upsert(t.Context(), unordered); err != nil {
		t.Fatalf("unordered fixture replacement: %v", err)
	}
	unordered.ObservedAt = observed
	if err := first.Upsert(t.Context(), unordered); err != nil {
		t.Fatalf("first ordered observation: %v", err)
	}
}

func TestAgentFsStoreExpiryRetainsBoundedObservationTombstones(t *testing.T) {
	root := t.TempDir()
	store := openAgentFsStore(t, root)
	cutoff := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	retained := fsInventoryAgent(1, cutoff.Add(time.Nanosecond))
	retained.LastHeartbeat = cutoff
	reactivated := retained.Clone()
	reactivated.ID = fsInventoryAgent(2, cutoff).ID
	old := fsInventoryAgent(3, cutoff)
	unordered := fsInventoryAgent(4, cutoff)
	unordered.ObservedAt = time.Time{}
	records := []agents.Agent{retained, reactivated, old, unordered}
	for _, record := range records {
		if err := store.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.DeleteExpired(t.Context(), cutoff)
	if err != nil || !reflect.DeepEqual(removed, records) {
		t.Fatalf("expired inventory = %#v, %v", removed, err)
	}
	for _, record := range records {
		if _, err := store.Get(t.Context(), record.ID); !errors.Is(err, agents.ErrUnknownAgent) {
			t.Fatalf("expired agent remained visible: %s, %v", record.ID, err)
		}
		_, err := os.Stat(filepath.Join(root, "agents", record.ID+".json"))
		if record.ObservedAt.After(cutoff) && err != nil {
			t.Fatalf("recent source mark was not retained: %v", err)
		}
		if !record.ObservedAt.After(cutoff) && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old source mark was not removed: %v", err)
		}
	}
	reopened := openAgentFsStore(t, root)
	visible, err := reopened.List(t.Context())
	if err != nil || len(visible) != 0 {
		t.Fatalf("reopened tombstones were visible: %#v, %v", visible, err)
	}
	if err := reopened.Upsert(t.Context(), retained); !errors.Is(err, agents.ErrStaleObservation) {
		t.Fatalf("replay revived expired agent after reopen: %v", err)
	}
	if removed, err := reopened.DeleteExpired(t.Context(), cutoff); err != nil || len(removed) != 0 {
		t.Fatalf("tombstones expired twice: %#v, %v", removed, err)
	}
	reactivated.ObservedAt = reactivated.ObservedAt.Add(time.Nanosecond)
	reactivated.LastHeartbeat = reactivated.ObservedAt
	if err := reopened.Upsert(t.Context(), reactivated); err != nil {
		t.Fatalf("fresh observation did not reactivate agent: %v", err)
	}
	if removed, err := store.DeleteExpired(t.Context(), retained.ObservedAt); err != nil || len(removed) != 0 {
		t.Fatalf("tombstone cleanup reported expiry again: %#v, %v", removed, err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", retained.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tombstone survived its observation cutoff: %v", err)
	}
	visible, err = store.List(t.Context())
	if err != nil || !reflect.DeepEqual(visible, []agents.Agent{reactivated}) {
		t.Fatalf("reactivated inventory = %#v, %v", visible, err)
	}
}

func TestAgentFsStoreConcurrentRenewalAndExpiry(t *testing.T) {
	root := t.TempDir()
	first, second := openAgentFsStore(t, root), openAgentFsStore(t, root)
	cutoff := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	for i := range 12 {
		old := fsInventoryAgent(i+1, cutoff.Add(time.Nanosecond))
		old.LastHeartbeat = cutoff
		if err := first.Upsert(t.Context(), old); err != nil {
			t.Fatal(err)
		}
		renewed := old.Clone()
		renewed.ObservedAt = old.ObservedAt.Add(time.Nanosecond)
		renewed.LastHeartbeat = cutoff.Add(time.Minute)
		gate, done := make(chan struct{}), make(chan error, 2)
		go func() {
			<-gate
			done <- second.Upsert(t.Context(), renewed)
		}()
		go func() {
			<-gate
			_, err := first.DeleteExpired(t.Context(), cutoff)
			done <- err
		}()
		close(gate)
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		stored, err := first.Get(t.Context(), renewed.ID)
		if err != nil || !reflect.DeepEqual(stored, renewed) {
			t.Fatalf("expiry lost concurrent renewal: %#v, %v", stored, err)
		}
		if err := first.Upsert(t.Context(), old); !errors.Is(err, agents.ErrStaleObservation) {
			t.Fatalf("old observation replaced concurrent renewal: %v", err)
		}
	}
}

func openAgentFsStore(t *testing.T, root string) *agentfs.AgentFsStore {
	t.Helper()
	store, err := agentfs.NewAgentFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func fsInventoryAgent(number int, observed time.Time) agents.Agent {
	return agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{
			ID: fmt.Sprintf("web-quality-agent-%08d", number), Type: "web-quality", Status: agents.AgentReady,
			Server: agents.ServerInfo{IPAddress: "192.0.2.1", Hostname: "worker-1"}, Config: []byte(`{}`),
		},
		ObservedAt: observed, LastSeen: observed.Add(time.Second), LastHeartbeat: observed,
	}
}
