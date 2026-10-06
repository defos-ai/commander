package providers_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"commander/framework/domain/controllers"
	controllerfs "commander/providers/fs/controllers"
)

func TestControllerFsStoreRoundTripAndOrdering(t *testing.T) {
	root := t.TempDir()
	first := openControllerFsStore(t, root)
	second := openControllerFsStore(t, root)
	record := fsController(1)
	record.Stale = true
	if err := first.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	record.Stale = false
	got, err := second.Get(t.Context(), record.ID)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("round trip = %#v, %v", got, err)
	}
	if snapshot := got.Snapshot(record.LeaseExpiresAt); snapshot.Status != controllers.Unavailable || !snapshot.Stale {
		t.Fatalf("expired controller = %#v", snapshot)
	}
	for _, stamp := range []time.Time{record.LastHeartbeat.Add(-time.Nanosecond), record.LastHeartbeat} {
		older := record
		older.LastHeartbeat, older.Status = stamp, controllers.Stopped
		if err := second.Upsert(t.Context(), older); !errors.Is(err, controllers.ErrStaleObservation) {
			t.Fatalf("out-of-order heartbeat accepted: %v", err)
		}
	}
	newer := record
	newer.LastHeartbeat = newer.LastHeartbeat.Add(time.Nanosecond)
	newer.Status, newer.StatusReason = controllers.Degraded, "broker unavailable"
	if err := second.Upsert(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	if err := first.Upsert(t.Context(), record); !errors.Is(err, controllers.ErrStaleObservation) {
		t.Fatalf("old instance overwrote newer heartbeat: %v", err)
	}
	third := openControllerFsStore(t, root)
	items, err := third.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []controllers.Controller{newer}) {
		t.Fatalf("reopened inventory = %#v, %v", items, err)
	}
	stopped := newer
	stopped.LastHeartbeat = stopped.LastHeartbeat.Add(time.Nanosecond)
	stopped.Status, stopped.StatusReason = controllers.Stopped, ""
	stopped.LeaseExpiresAt = stopped.LastHeartbeat
	if err := third.Upsert(t.Context(), stopped); err != nil {
		t.Fatal(err)
	}
	got, err = first.Get(t.Context(), record.ID)
	if err != nil || got.Snapshot(stopped.LastHeartbeat.Add(time.Hour)).Status != controllers.Stopped {
		t.Fatalf("stopped controller = %#v, %v", got, err)
	}
}

func TestControllerFsStoreConcurrentRegistrations(t *testing.T) {
	root := t.TempDir()
	stores := []controllers.ControllerStore{openControllerFsStore(t, root), openControllerFsStore(t, root)}
	const count = 24
	start := make(chan struct{})
	results := make(chan error, count*2)
	for i := range count {
		for revision := range 2 {
			go func() {
				<-start
				record := fsController(i)
				record.LastHeartbeat = record.LastHeartbeat.Add(time.Duration(revision) * time.Nanosecond)
				results <- stores[revision].Upsert(t.Context(), record)
			}()
		}
	}
	close(start)
	for range count * 2 {
		if err := <-results; err != nil && !errors.Is(err, controllers.ErrStaleObservation) {
			t.Error(err)
		}
	}
	items, err := stores[0].List(t.Context())
	if err != nil || len(items) != count {
		t.Fatalf("concurrent inventory contains %d records, %v", len(items), err)
	}
	for i, got := range items {
		want := fsController(i)
		want.LastHeartbeat = want.LastHeartbeat.Add(time.Nanosecond)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("lost update or unsorted inventory: got %#v, want %#v", got, want)
		}
	}
}

func TestControllerFsStoreSortsByID(t *testing.T) {
	catalog := openControllerFsStore(t, t.TempDir())
	short := fsController(1)
	short.ID, short.Type = "a-controller-12345678", "a"
	long := short
	long.ID, long.Type = "a-controller-12345678-controller-12345678", "a-controller-12345678"
	for _, record := range []controllers.Controller{long, short} {
		if err := catalog.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []controllers.Controller{short, long}) {
		t.Fatalf("inventory sorted filenames instead of IDs: %#v, %v", items, err)
	}
}

func TestControllerFsStoreRejectedWritesPreserveRecords(t *testing.T) {
	root := t.TempDir()
	catalog := openControllerFsStore(t, root)
	record := fsController(1)
	if err := catalog.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*controllers.Controller){
		func(r *controllers.Controller) { r.ID = "../invalid" },
		func(r *controllers.Controller) { r.Type = "other" },
		func(r *controllers.Controller) { r.LastHeartbeat = time.Time{} },
		func(r *controllers.Controller) { r.LeaseExpiresAt = r.LastHeartbeat },
	} {
		invalid := record
		change(&invalid)
		if err := catalog.Upsert(t.Context(), invalid); err == nil {
			t.Fatalf("invalid registration accepted: %#v", invalid)
		}
	}
	for _, missing := range []string{"../invalid", "default-controller-ffffffff"} {
		if _, err := catalog.Get(t.Context(), missing); !errors.Is(err, controllers.ErrUnknownController) {
			t.Fatalf("missing %q = %v", missing, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := catalog.Upsert(cancelled, record); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write = %v", err)
	}
	if _, err := catalog.Get(cancelled, record.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
	if _, err := catalog.List(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list = %v", err)
	}
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []controllers.Controller{record}) {
		t.Fatalf("rejected writes changed records: %#v, %v", items, err)
	}
	path := filepath.Join(root, "controllers", record.ID+".json")
	if err := os.WriteFile(path, []byte(`[{"id":"broken"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Upsert(t.Context(), record); err == nil {
		t.Fatal("write replaced a malformed registration")
	}
	if _, err := controllerfs.NewControllerFsStore(root); err == nil {
		t.Fatal("reopened malformed inventory")
	}
}

func openControllerFsStore(t *testing.T, root string) *controllerfs.ControllerFsStore {
	t.Helper()
	catalog, err := controllerfs.NewControllerFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func fsController(index int) controllers.Controller {
	heartbeat := time.Date(2026, 10, 6, 12, 0, 0, 123456789, time.UTC)
	return controllers.Controller{
		ID: fmt.Sprintf("default-controller-%08x", index), Type: "default", Status: controllers.Ready,
		Server: controllers.ServerInfo{Hostname: "worker-1", OS: "linux", Arch: "amd64"}, Version: "v1", AgentSelector: "*",
		StartedAt: heartbeat.Add(-time.Minute), LastHeartbeat: heartbeat, LeaseExpiresAt: heartbeat.Add(45 * time.Second),
	}
}
