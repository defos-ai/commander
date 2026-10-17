package mongo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	mongoagents "github.com/defos-ai/commander/providers/mongo/agents"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestAgentMongoStore(t *testing.T) {
	catalog, err := mongoagents.NewAgentMongoStore(t.Context(), openStore(t, "agents"))
	if err != nil {
		t.Fatal(err)
	}
	original := inventoryAgent(2, time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC))
	original.Status = agents.AgentWorking
	original.Server = agents.ServerInfo{IPAddress: "2001:db8::1", Hostname: "review-pod-1"}
	original.Responsibilities = []string{"Review websites"}
	original.DiscoveryScope = &agents.DiscoveryScope{Paths: []string{}, Services: []string{"browser"}}
	original.Config = json.RawMessage(`{"cursor":18446744073709551615,"nested":{"enabled":true}}`)
	original.Actions = []actions.Action{
		{Name: "capture-page", Status: actions.Ready, RequiredSecrets: []string{"BROWSER_API_KEY"}, InputSchema: json.RawMessage(`{"type":"object","x-example":9007199254740993}`), OutputSchema: json.RawMessage(`{}`)},
		{Name: "lookup", Status: actions.Unavailable, StatusReason: "API_KEY missing", MissingRequirements: []actions.Requirement{{Kind: actions.ConfigurationRequirement, Name: "API_KEY", Sensitive: true}}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
	}
	original.LeaseExpiresAt = original.LastHeartbeat.Add(45 * time.Second)
	other := original.Clone()
	other.ID, other.Status, other.Stale = "browser-agent-00000001", agents.AgentStopped, true
	other.DiscoveryScope.Paths = nil
	items, err := catalog.List(t.Context())
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty inventory = %#v, error = %v", items, err)
	}

	for _, record := range []agents.Agent{original, other} {
		if err := catalog.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	stored, err := catalog.Get(t.Context(), original.ID)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatalf("snapshot changed on round trip: %#v, error = %v", stored, err)
	}
	items, err = catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{other, original}) {
		t.Fatalf("inventory changed content, status or order: %#v, error = %v", items, err)
	}
	stored.Actions[0].InputSchema[0] = '!'
	stored.Actions[0].RequiredSecrets[0] = "changed"
	stored.Actions[1].MissingRequirements[0].Name = "changed"
	stored.DiscoveryScope.Services[0] = "changed"
	stored.Config[0] = '!'
	stored.Responsibilities[0] = "changed"
	stored, err = catalog.Get(t.Context(), original.ID)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatalf("reader mutated a persisted snapshot: %#v, error = %v", stored, err)
	}

	updated := inventoryAgent(2, time.Time{})
	if err := catalog.Upsert(t.Context(), updated); err != nil {
		t.Fatal(err)
	}

	items, err = catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{other, updated}) {
		t.Fatalf("replacement retained fields or changed another agent: %#v, error = %v", items, err)
	}
}

func TestAgentMongoStoreOrdersObservationsAcrossRestart(t *testing.T) {
	database := openStore(t, "agents")
	catalog, err := mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)
	original := inventoryAgent(1, observed.Add(-time.Hour))
	if err := catalog.Upsert(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	original.ObservedAt = observed
	if err := catalog.Upsert(t.Context(), original); err != nil {
		t.Fatalf("nonzero observation did not replace a zero observation: %v", err)
	}
	catalog, err = mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := catalog.Get(t.Context(), original.ID)
	if err != nil || !reflect.DeepEqual(stored, original) {
		t.Fatalf("restart changed observation precision or content: %#v, error = %v", stored, err)
	}
	for _, timestamp := range []time.Time{time.Time{}, observed.Add(-time.Nanosecond), observed} {
		rejected := original.Clone()
		rejected.ObservedAt = timestamp
		rejected.LastSeen = observed.Add(time.Hour)
		rejected.LastHeartbeat = observed.Add(time.Hour)
		rejected.Status = agents.AgentStopped
		if err := catalog.Upsert(t.Context(), rejected); !errors.Is(err, agents.ErrStaleObservation) {
			t.Fatalf("observation %s was not rejected as stale: %v", timestamp, err)
		}
		stored, err := catalog.Get(t.Context(), original.ID)
		if err != nil || !reflect.DeepEqual(stored, original) {
			t.Fatalf("stale observation changed the saved agent: %#v, error = %v", stored, err)
		}
	}
	updated := original.Clone()
	updated.ObservedAt = observed.Add(time.Nanosecond)
	updated.LastSeen = original.LastSeen.Add(-time.Second)
	updated.LastHeartbeat = original.LastHeartbeat.Add(-time.Second)
	updated.Status = agents.AgentStopped
	if err := catalog.Upsert(t.Context(), updated); err != nil {
		t.Fatalf("strictly newer observation rejected: %v", err)
	}
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{updated}) {
		t.Fatalf("newer observation did not replace the saved agent: %#v, error = %v", items, err)
	}
}

func TestAgentMongoStoreExpiredObservationsBlockStaleReactivation(t *testing.T) {
	database := openStore(t, "agents")
	catalog, err := mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)
	original := inventoryAgent(1, cutoff)
	original.ObservedAt = cutoff.Add(2 * time.Nanosecond)
	if err := catalog.Upsert(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	removed, err := catalog.DeleteExpired(t.Context(), cutoff)
	if err != nil || !reflect.DeepEqual(removed, []agents.Agent{original}) {
		t.Fatalf("expiry changed the saved observation: %#v, error = %v", removed, err)
	}
	var expired bson.M
	if err := database.GetByID(t.Context(), "agents", original.ID, &expired); err != nil || expired["expired"] != true {
		t.Fatalf("newer observation was not retained as an expired record: %#v, error = %v", expired, err)
	}
	catalog, err = mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Get(t.Context(), original.ID); !errors.Is(err, agents.ErrUnknownAgent) {
		t.Fatalf("expired record remained visible after restart: %v", err)
	}
	items, err := catalog.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("expired record appeared in inventory after restart: %#v, error = %v", items, err)
	}
	for _, timestamp := range []time.Time{time.Time{}, original.ObservedAt.Add(-time.Nanosecond), original.ObservedAt} {
		rejected := original.Clone()
		rejected.ObservedAt = timestamp
		rejected.LastHeartbeat = cutoff.Add(time.Hour)
		rejected.LastSeen = cutoff.Add(time.Hour)
		if err := catalog.Upsert(t.Context(), rejected); !errors.Is(err, agents.ErrStaleObservation) {
			t.Fatalf("observation %s reactivated an expired agent: %v", timestamp, err)
		}
		var document bson.M
		if err := database.GetByID(t.Context(), "agents", original.ID, &document); err != nil || !reflect.DeepEqual(document, expired) {
			t.Fatalf("stale observation changed the expired record: %#v, error = %v", document, err)
		}
	}
	if removed, err := catalog.DeleteExpired(t.Context(), original.ObservedAt.Add(-time.Nanosecond)); err != nil || len(removed) != 0 {
		t.Fatalf("later sweep returned a retained expired record: %#v, error = %v", removed, err)
	}
	updated := original.Clone()
	updated.ObservedAt = original.ObservedAt.Add(time.Nanosecond)
	updated.LastHeartbeat = cutoff.Add(time.Hour)
	if err := catalog.Upsert(t.Context(), updated); err != nil {
		t.Fatalf("strictly newer observation did not reactivate the agent: %v", err)
	}
	stored, err := catalog.Get(t.Context(), original.ID)
	if err != nil || !reflect.DeepEqual(stored, updated) {
		t.Fatalf("reactivated agent changed: %#v, error = %v", stored, err)
	}
	items, err = catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{updated}) {
		t.Fatalf("reactivated agent was not listed: %#v, error = %v", items, err)
	}
}

func TestAgentMongoStorePrunesExpiredObservations(t *testing.T) {
	database := openStore(t, "agents")
	catalog, err := mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)
	expected := make(map[string]agents.Agent)
	var retained agents.Agent
	for i, observed := range []time.Time{time.Time{}, cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(time.Nanosecond)} {
		record := inventoryAgent(i, cutoff)
		record.ObservedAt = observed
		if err := catalog.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		expected[record.ID] = record
		if observed.After(cutoff) {
			retained = record
		}
	}
	removed, err := catalog.DeleteExpired(t.Context(), cutoff)
	if err != nil || len(removed) != len(expected) {
		t.Fatalf("expired records = %#v, error = %v", removed, err)
	}
	for _, record := range removed {
		if want, ok := expected[record.ID]; !ok || !reflect.DeepEqual(record, want) {
			t.Fatalf("expiry changed or duplicated an observation: %#v", record)
		}
		delete(expected, record.ID)
		var document bson.M
		err := database.GetByID(t.Context(), "agents", record.ID, &document)
		if record.ID == retained.ID {
			if err != nil || document["expired"] != true {
				t.Fatalf("newer observation was not retained: %#v, error = %v", document, err)
			}
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			t.Fatalf("observation at or before cutoff was not physically deleted: %s, error = %v", record.ID, err)
		}
	}
	catalog, err = mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	if removed, err := catalog.DeleteExpired(t.Context(), cutoff); err != nil || len(removed) != 0 {
		t.Fatalf("repeated sweep returned an expired record: %#v, error = %v", removed, err)
	}
	var document bson.M
	if err := database.GetByID(t.Context(), "agents", retained.ID, &document); err != nil || document["expired"] != true {
		t.Fatalf("sweep pruned an observation newer than cutoff: %#v, error = %v", document, err)
	}
	if removed, err := catalog.DeleteExpired(t.Context(), retained.ObservedAt); err != nil || len(removed) != 0 {
		t.Fatalf("cleanup returned an already expired record: %#v, error = %v", removed, err)
	}
	if err := database.GetByID(t.Context(), "agents", retained.ID, &document); !errors.Is(err, mongo.ErrNoDocuments) {
		t.Fatalf("expired observation at cutoff was not physically deleted: %v", err)
	}
	items, err := catalog.List(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("cleanup left active agents: %#v, error = %v", items, err)
	}
}

func TestAgentMongoStoreRejectsInvalidAndCancelledWrites(t *testing.T) {
	database := openStore(t, "agents")
	catalog, err := mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	original := inventoryAgent(1, time.Date(2026, 9, 30, 12, 0, 0, 1, time.UTC))
	if err := catalog.Upsert(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"invalid/id", "browser-agent-00000002"} {
		if _, err := catalog.Get(t.Context(), missing); !errors.Is(err, agents.ErrUnknownAgent) {
			t.Fatalf("missing agent %q: %v", missing, err)
		}
	}
	for _, change := range []func(*agents.Agent){
		func(a *agents.Agent) { a.ID = "invalid/id" },
		func(a *agents.Agent) { a.LastSeen = time.Time{} },
		func(a *agents.Agent) { a.Server.IPAddress = "not-an-ip" },
		func(a *agents.Agent) { a.Server.Hostname = "invalid hostname" },
		func(a *agents.Agent) { a.Config = json.RawMessage(`{`) },
		func(a *agents.Agent) {
			a.Actions = []actions.Action{{Name: "inspect", Status: actions.Ready, InputSchema: json.RawMessage(`{`), OutputSchema: json.RawMessage(`{}`)}}
		},
		func(a *agents.Agent) {
			a.Actions = []actions.Action{{Name: "inspect", Status: actions.Ready, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{`)}}
		},
	} {
		invalid := original.Clone()
		change(&invalid)

		if err := catalog.Upsert(t.Context(), invalid); err == nil {
			t.Fatalf("invalid inventory observation accepted: %#v", invalid)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	updated := original.Clone()
	updated.Status = agents.AgentStopped

	if _, err := mongoagents.NewAgentMongoStore(cancelled, database); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled constructor: %v", err)
	}
	if err := catalog.Upsert(cancelled, updated); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upsert: %v", err)
	}
	if _, err := catalog.Get(cancelled, original.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get: %v", err)
	}
	if _, err := catalog.List(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list: %v", err)
	}
	if _, err := catalog.DeleteExpired(cancelled, original.LastHeartbeat); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled expiry: %v", err)
	}
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{original}) {
		t.Fatalf("rejected writes changed inventory: %#v, error = %v", items, err)
	}
}

func TestAgentMongoStoreExpiresOnlyHeartbeatLeases(t *testing.T) {
	catalog, err := mongoagents.NewAgentMongoStore(t.Context(), openStore(t, "agents"))
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)
	expected := map[string]agents.Agent{}
	var fresh agents.Agent
	for i, heartbeat := range []time.Time{time.Time{}, cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(time.Nanosecond)} {
		record := inventoryAgent(i, heartbeat)
		record.LastSeen = cutoff.Add(time.Hour)
		record.LeaseExpiresAt = cutoff.Add(2 * time.Hour)
		if i == 0 {
			record.Status = agents.AgentStopped
		}
		if err := catalog.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			expected[record.ID] = record
		} else {
			fresh = record
		}
	}

	removed, err := catalog.DeleteExpired(t.Context(), cutoff)

	if err != nil || len(removed) != len(expected) {
		t.Fatalf("expired records = %#v, error = %v", removed, err)
	}
	for _, record := range removed {
		if want, ok := expected[record.ID]; !ok || !reflect.DeepEqual(record, want) {
			t.Fatalf("expiry changed or duplicated an observation: %#v", record)
		}
		delete(expected, record.ID)
		if _, err := catalog.Get(t.Context(), record.ID); !errors.Is(err, agents.ErrUnknownAgent) {
			t.Fatalf("expired agent still exists: %s, error = %v", record.ID, err)
		}
	}
	items, err := catalog.List(t.Context())
	if err != nil || !reflect.DeepEqual(items, []agents.Agent{fresh}) {
		t.Fatalf("expiry deleted a fresh heartbeat or changed its precision: %#v, error = %v", items, err)
	}
	if removed, err := catalog.DeleteExpired(t.Context(), cutoff); err != nil || len(removed) != 0 {
		t.Fatalf("repeated sweep returned removed agents: %#v, error = %v", removed, err)
	}
}

func TestConcurrentAgentExpiryAndHeartbeats(t *testing.T) {
	database := openStore(t, "agents")
	first, err := mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongoagents.NewAgentMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	catalogs := []agents.AgentStore{first, second}
	cutoff := time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)
	const count = 24
	type sweep struct {
		removed []agents.Agent
		err     error
	}

	for round, renew := range []bool{false, true} {
		observed := cutoff.Add(time.Duration(round+1) * time.Nanosecond)
		for i := range count {
			record := inventoryAgent(i, cutoff)
			record.ObservedAt = observed
			if err := first.Upsert(t.Context(), record); err != nil {
				t.Fatal(err)
			}
		}
		start := make(chan struct{})
		swept := make(chan sweep, len(catalogs))
		written := make(chan error, count)
		for _, catalog := range catalogs {
			go func() {
				<-start
				removed, err := catalog.DeleteExpired(t.Context(), cutoff)
				swept <- sweep{removed: removed, err: err}
			}()
		}
		if renew {
			for i := range count {
				go func() {
					<-start
					record := inventoryAgent(i, cutoff.Add(time.Nanosecond))
					record.ObservedAt = observed.Add(time.Nanosecond)
					written <- catalogs[i%len(catalogs)].Upsert(t.Context(), record)
				}()
			}
		}

		close(start)
		seen := make(map[string]bool, count)
		for range catalogs {
			result := <-swept
			if result.err != nil {
				t.Error(result.err)
			}
			for _, record := range result.removed {
				if seen[record.ID] || record.LastHeartbeat.After(cutoff) {
					t.Errorf("sweeps returned a duplicate or renewed agent: %#v", record)
				}
				seen[record.ID] = true
			}
		}
		if renew {
			for range count {
				if err := <-written; err != nil {
					t.Error(err)
				}
			}
		} else if len(seen) != count {
			t.Fatalf("concurrent sweeps removed %d agents, want %d", len(seen), count)
		}

		items, err := first.List(t.Context())
		if err != nil || !renew && len(items) != 0 || renew && len(items) != count {
			t.Fatalf("renew=%t: inventory after races = %#v, error = %v", renew, items, err)
		}
		for i, record := range items {
			want := inventoryAgent(i, cutoff.Add(time.Nanosecond))
			want.ObservedAt = observed.Add(time.Nanosecond)
			if !reflect.DeepEqual(record, want) {
				t.Fatalf("sweep lost or changed a fresh heartbeat: %#v", record)
			}
		}
	}
}

func inventoryAgent(index int, heartbeat time.Time) agents.Agent {
	return agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: fmt.Sprintf("browser-agent-%08x", index), Type: "browser-agent", Status: agents.AgentReady},
		LastSeen:      time.Date(2026, 9, 30, 12, 0, 1, 123456789, time.UTC),
		LastHeartbeat: heartbeat,
	}
}
