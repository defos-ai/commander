package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"commander/framework/domain/agents"
	"commander/framework/domain/missions"
	fsmissions "commander/providers/fs/missions"
)

func TestMissionsCatalogReflectsStoredDefinitions(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("API_SWAGGER_ENABLED", "true")
	store, err := fsmissions.NewMissionFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	mission := missions.Mission{
		Name: "review-website", Objective: "Review a website",
		RequiredActions: []string{"discover-pages", "check-links"}, StartingActions: []string{"discover-pages"},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		Automations: map[string]missions.Automation{
			"discover-pages": {Run: []string{"check-links"}, Continue: missions.WhenAll},
			"check-links":    {Decide: missions.Model},
		},
		Steps: []missions.Step{{ID: "links", Objective: "Check discovered links", AgentTypes: []agents.AgentType{"web-quality"}, RequiredActions: []string{"check-links"}}},
	}
	if err := store.Upsert(t.Context(), mission); err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, fixture.localArgs())

	result := server.request(t, http.MethodGet, "/api/v1/missions/review-website", authenticate)
	detail := decodeResponse[missions.Mission](t, result)
	assertResponseSchema(t, server, "Mission", result)

	if !reflect.DeepEqual(detail, mission) {
		t.Fatalf("mission detail lost definition fields: got %+v, want %+v", detail, mission)
	}
	mission.Name = "audit/links"
	mission.Objective = "Check only website links"
	if err := store.Upsert(t.Context(), mission); err != nil {
		t.Fatal(err)
	}

	list := decodeResponse[struct {
		Items []missions.Mission `json:"items"`
	}](t, server.request(t, http.MethodGet, "/api/v1/missions", authenticate))

	if len(list.Items) != 2 || list.Items[0].Name != "audit/links" || list.Items[1].Name != "review-website" {
		t.Fatalf("mission catalog did not reflect the live store in name order: %+v", list.Items)
	}
	escaped := decodeResponse[missions.Mission](t, server.request(t, http.MethodGet, "/api/v1/missions/"+url.PathEscape(mission.Name), authenticate))
	if !reflect.DeepEqual(escaped, mission) {
		t.Fatalf("escaped mission name did not resolve: got %+v, want %+v", escaped, mission)
	}
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/missions/unknown", authenticate), http.StatusNotFound, "mission_not_found")
	assertAPIError(t, server.request(t, http.MethodGet, "/api/v1/missions"), http.StatusUnauthorized, "authentication_required")
	if result := server.request(t, http.MethodPost, "/api/v1/missions", authenticate); result.status != http.StatusMethodNotAllowed {
		t.Fatalf("catalog accepted a mutation: status=%d body=%s", result.status, result.body)
	}
}

func TestCatalogEmptyResultsAndStoreFailures(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	for _, path := range []string{"missions", "actions", "agents"} {
		result := decodeResponse[map[string]json.RawMessage](t, server.request(t, http.MethodGet, "/api/v1/"+path, authenticate))
		if len(result) != 1 || string(result["items"]) != "[]" {
			t.Fatalf("empty %s catalog must contain an empty array: %s", path, result["items"])
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "database", "missions.json"), []byte("private-store-content-not-json"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/v1/missions", "/api/v1/missions/unknown"} {
		assertAPIError(t, server.request(t, http.MethodGet, path, authenticate), http.StatusInternalServerError, "internal_error")
	}
	assertHealthy(t, server.request(t, http.MethodGet, "/healthz"))
}
