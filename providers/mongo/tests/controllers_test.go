package mongo_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/controllers"
	mongocontrollers "github.com/defos-ai/commander/providers/mongo/controllers"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestControllerMongoStoreRoundTripAndOrdering(t *testing.T) {
	database := openStore(t, "controllers")
	first, err := mongocontrollers.NewControllerMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongocontrollers.NewControllerMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	record := mongoController(1)
	record.Stale = true
	if err := first.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	record.Stale = false
	got, err := second.Get(t.Context(), record.ID)
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("round trip = %#v, %v", got, err)
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
	items, err := first.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []controllers.Controller{newer}) {
		t.Fatalf("inventory = %#v, %v", items, err)
	}
	stopped := newer
	stopped.LastHeartbeat = stopped.LastHeartbeat.Add(time.Nanosecond)
	stopped.Status, stopped.StatusReason = controllers.Stopped, ""
	stopped.LeaseExpiresAt = stopped.LastHeartbeat
	if err := second.Upsert(t.Context(), stopped); err != nil {
		t.Fatal(err)
	}
	got, err = first.Get(t.Context(), record.ID)
	if err != nil || !reflect.DeepEqual(got, stopped) {
		t.Fatalf("stopped controller = %#v, %v", got, err)
	}
}

func TestControllerMongoStoreConcurrentRegistrations(t *testing.T) {
	database := openStore(t, "controllers")
	first, err := mongocontrollers.NewControllerMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongocontrollers.NewControllerMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	stores := []controllers.ControllerStore{first, second}
	const count = 24
	start := make(chan struct{})
	results := make(chan error, count*2)
	for i := range count {
		for revision := range 2 {
			go func() {
				<-start
				record := mongoController(i)
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
	items, err := first.List(t.Context())
	if err != nil || len(items) != count {
		t.Fatalf("concurrent inventory contains %d records, %v", len(items), err)
	}
	for i, got := range items {
		want := mongoController(i)
		want.LastHeartbeat = want.LastHeartbeat.Add(time.Nanosecond)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("lost update or unsorted inventory: got %#v, want %#v", got, want)
		}
	}
}

func TestControllerMongoStoreRejectedWritesPreserveRecords(t *testing.T) {
	database := openStore(t, "controllers")
	catalog, err := mongocontrollers.NewControllerMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	record := mongoController(1)
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
	if _, err := mongocontrollers.NewControllerMongoStore(cancelled, database); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled constructor = %v", err)
	}
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
	if _, err := database.Update(t.Context(), "controllers", bson.M{"_id": record.ID}, bson.M{"$set": bson.M{"last_heartbeat.nanoseconds": -1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get(t.Context(), record.ID); err == nil {
		t.Fatal("read accepted malformed timestamp")
	}
}

func mongoController(index int) controllers.Controller {
	heartbeat := time.Date(2026, 10, 6, 12, 0, 0, 123456789, time.UTC)
	return controllers.Controller{
		ID: fmt.Sprintf("default-controller-%08x", index), Type: "default", Status: controllers.Ready,
		Server: controllers.ServerInfo{Hostname: "worker-1", OS: "linux", Arch: "amd64"}, Version: "v1", AgentSelector: "*", RuleSelector: "mission-rules",
		StartedAt: heartbeat.Add(-time.Minute), LastHeartbeat: heartbeat, LeaseExpiresAt: heartbeat.Add(45 * time.Second),
	}
}
