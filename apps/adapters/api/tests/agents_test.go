package api_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"commander/framework/domain/actions"
	"commander/framework/domain/agents"
	fsagents "commander/providers/fs/agents"
)

func TestAgentsShowCurrentWorkReadinessAndLeaseExpiry(t *testing.T) {
	fixture := newFixture(t)
	store, err := fsagents.NewAgentFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	working := agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{
			ID: "web-quality-agent-bbbbbbbb", Type: "web-quality", Status: agents.AgentWorking,
			Config:  json.RawMessage(`{}`),
			Work:    &agents.Work{OperationID: "operation", TaskID: "task", CommandID: "command", ActionName: "check-links"},
			Server:  agents.ServerInfo{IPAddress: "10.0.0.3", Hostname: "agent-pod"},
			Actions: []actions.Action{{Name: "capture-page", Status: actions.Unavailable, StatusReason: "Browser missing", MissingRequirements: []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "chromium"}}, InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`)}},
		},
		ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour),
	}
	stale := working.Clone()
	stale.ID = "web-quality-agent-aaaaaaaa"
	stale.Status = agents.AgentReady
	stale.Work = nil
	stale.LeaseExpiresAt = now.Add(-time.Second)
	for _, agent := range []agents.Agent{working, stale} {
		if err := store.Upsert(t.Context(), agent); err != nil {
			t.Fatal(err)
		}
	}
	server := startAPI(t, fixture.localArgs())

	list := decodeResponse[struct {
		Items []agents.Agent `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/agents", authenticate))
	busy := decodeResponse[agents.Agent](t, server.request(t, http.MethodGet, "/api/v1/agents/"+working.ID, authenticate))
	expired := decodeResponse[agents.Agent](t, server.request(t, http.MethodGet, "/api/v1/agents/"+stale.ID, authenticate))

	if len(list.Items) != 2 || list.Items[0].ID != stale.ID || list.Items[1].ID != working.ID {
		t.Fatalf("unexpected agent catalog order: %+v", list.Items)
	}
	if !reflect.DeepEqual(busy, working) {
		t.Fatalf("agent detail lost work, host, or action readiness: got %+v, want %+v", busy, working)
	}
	if !expired.Stale || expired.Status != agents.AgentUnavailable || !list.Items[0].Stale || list.Items[0].Status != agents.AgentUnavailable {
		t.Fatalf("expired lease still appears ready: detail=%+v list=%+v", expired, list.Items[0])
	}
	stored, err := store.Get(t.Context(), stale.ID)
	if err != nil || stored.Stale || stored.Status != agents.AgentReady {
		t.Fatalf("reading the API changed persisted agent state: %+v, %v", stored, err)
	}
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/agents/web-quality-agent-cccccccc", authenticate), http.StatusNotFound, "agent_not_found")
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/agents"), http.StatusUnauthorized, "authentication_required")
}
