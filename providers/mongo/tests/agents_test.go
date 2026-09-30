package mongo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	mongoagents "commander/providers/mongo/controller/agents"
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
	original.GrantedPermissions = []string{"network-access", "execute-command"}
	original.DiscoveryScope = &agents.DiscoveryScope{Paths: []string{}, Services: []string{"browser"}}
	original.Config = json.RawMessage(`{"cursor":18446744073709551615,"nested":{"enabled":true}}`)
	original.Actions = []actions.Action{
		{Name: "capture-page", Status: actions.Ready, RequiredPermissions: []string{"execute-command"}, InputSchema: json.RawMessage(`{"type":"object","x-example":9007199254740993}`), OutputSchema: json.RawMessage(`{}`)},
		{Name: "lookup", Status: actions.Unavailable, StatusReason: "API_KEY missing", MissingRequirements: []actions.Requirement{{Kind: actions.ConfigurationRequirement, Name: "API_KEY", Secret: true}}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)},
	}
	original.LeaseExpiresAt = original.LastHeartbeat.Add(45 * time.Second)
	other := original.Clone()
	other.ID, other.Status, other.Stale = "web-quality-agent-00000001", agents.AgentStopped, true
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
	stored.Actions[0].RequiredPermissions[0] = "changed"
	stored.Actions[1].MissingRequirements[0].Name = "changed"
	stored.DiscoveryScope.Services[0] = "changed"
	stored.Config[0] = '!'
	stored.GrantedPermissions[0] = "changed"
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
	for _, missing := range []string{"invalid/id", "web-quality-agent-00000002"} {
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
	first, err := mongoagents.NewAgentMongoStore(t.Context(), openStore(t, "agents"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := mongoagents.NewAgentMongoStore(t.Context(), openStore(t, "agents"))
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

	for _, renew := range []bool{false, true} {
		for i := range count {
			if err := first.Upsert(t.Context(), inventoryAgent(i, cutoff)); err != nil {
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
					written <- catalogs[i%len(catalogs)].Upsert(t.Context(), inventoryAgent(i, cutoff.Add(time.Nanosecond)))
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
			if !reflect.DeepEqual(record, inventoryAgent(i, cutoff.Add(time.Nanosecond))) {
				t.Fatalf("sweep lost or changed a fresh heartbeat: %#v", record)
			}
		}
	}
}

func inventoryAgent(index int, heartbeat time.Time) agents.Agent {
	return agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: fmt.Sprintf("web-quality-agent-%08x", index), Type: "web-quality", Status: agents.AgentReady},
		LastSeen:      time.Date(2026, 9, 30, 12, 0, 1, 123456789, time.UTC),
		LastHeartbeat: heartbeat,
	}
}
