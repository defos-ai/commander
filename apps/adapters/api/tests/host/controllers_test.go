package api_test

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/framework/domain/controllers"
	"github.com/defos-ai/commander/framework/id"
	fscontrollers "github.com/defos-ai/commander/providers/fs/controllers"
)

// The running controller renews its lease in Compose. Expired registrations
// therefore need a local store to check the HTTP projection and read-only rule.
func TestControllerQueriesProjectExpiredLeasesWithoutWriting(t *testing.T) {
	fixture := newFixture(t)
	store, err := fscontrollers.NewControllerFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, fixture.localArgs())
	empty := server.request(t, http.MethodGet, "/api/v1/controllers", authenticate)
	if empty.status != http.StatusOK || empty.body != "{\"items\":[]}\n" {
		t.Fatalf("empty controller inventory: %+v", empty)
	}
	now := time.Now().UTC()
	for _, status := range []controllers.Status{controllers.Ready, controllers.Stopped} {
		controllerID, err := id.ControllerID("default")
		if err != nil {
			t.Fatal(err)
		}
		record := controllers.Controller{
			ID: controllerID, Type: "default", Status: status, StatusReason: "observed state",
			Server:  controllers.ServerInfo{Hostname: "controller-host", OS: "linux", Arch: "amd64"},
			Version: "test-version", AgentSelector: "custom-agents", RuleSelector: "custom-rules", StartedAt: now.Add(-time.Hour),
			LastHeartbeat: now.Add(-time.Minute), LeaseExpiresAt: now.Add(-time.Second),
		}
		if err := store.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		detail := decodeResponse[models.ControllerResponse](t, server.request(t, http.MethodGet, "/api/v1/controllers/"+record.ID, authenticate))
		wantStatus, wantStale := "unavailable", true
		if status == controllers.Stopped {
			wantStatus, wantStale = "stopped", false
		}
		if detail.Status != wantStatus || detail.Stale != wantStale || detail.StatusReason != record.StatusReason {
			t.Fatalf("HTTP lease projection differs: %+v", detail)
		}
		if detail.AgentSelector != record.AgentSelector || detail.RuleSelector != record.RuleSelector {
			t.Fatalf("HTTP query lost the controller's selection policies: %+v", detail)
		}
		inventory := decodeResponse[models.ListResponse[models.ControllerResponse]](t, server.request(t, http.MethodGet, "/api/v1/controllers", authenticate))
		found := false
		for _, item := range inventory.Items {
			if item.ID == detail.ID {
				found = true
				if item != detail {
					t.Fatalf("list and detail projections differ: list=%+v detail=%+v", item, detail)
				}
			}
		}
		if !found {
			t.Fatal("saved controller is absent from inventory")
		}
		saved, err := store.Get(t.Context(), record.ID)
		if err != nil || saved != record {
			t.Fatalf("HTTP query changed saved observation: saved=%+v original=%+v err=%v", saved, record, err)
		}
	}
	// Readiness continues to report metadata readability despite stale controllers.
	assertHealthy(t, server.request(t, http.MethodGet, "/readyz"))
}
