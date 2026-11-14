package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func TestAgentDependencyGuidanceSurvivesReadinessChangesThroughAPI(t *testing.T) {
	fixture := hostfixtures.New(t)
	inventory := hostfixtures.NewAgentInventory(t, fixture.Root)
	record := hostfixtures.AgentDependencyRecord()
	legacy := record.Clone()
	legacy.ID, legacy.Type, legacy.Dependencies = "legacy-agent-00000001", "legacy-agent", nil
	for _, item := range []agents.Agent{record, legacy} {
		if err := inventory.Upsert(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	server := hostfixtures.Start(t, append(fixture.LocalArgs(), "--swagger"))
	path := "/api/v1/agents/" + record.ID

	response := server.Request(t, http.MethodGet, path, hostfixtures.Authenticate)
	detail := decodeResponse[models.AgentResponse](t, response)
	listed := decodeResponse[models.ListResponse[models.AgentResponse]](t, server.Request(t, http.MethodGet, "/api/v1/agents", hostfixtures.Authenticate))

	if len(detail.Dependencies) != 2 || detail.Dependencies[0].Name != "lightpanda" ||
		detail.Dependencies[0].Description != record.Dependencies[0].Description || detail.Dependencies[0].Version != "v1.2.3" {
		t.Fatalf("agent detail lost dependency installation guidance: %+v", detail.Dependencies)
	}
	if len(detail.Actions) != 1 || detail.Actions[0].Status != "unavailable" || len(detail.Actions[0].MissingRequirements) != 1 ||
		detail.Actions[0].MissingRequirements[0].Kind != "executable" || detail.Actions[0].MissingRequirements[0].Name != detail.Dependencies[0].Name {
		t.Fatalf("missing executable no longer identifies its dependency: %+v", detail.Actions)
	}
	if len(listed.Items) != 2 || listed.Items[0].ID != record.ID || !slices.Equal(listed.Items[0].Dependencies, detail.Dependencies) || len(listed.Items[1].Dependencies) != 0 {
		t.Fatalf("agent list changed declared dependencies or legacy records: %+v", listed.Items)
	}
	var encoded struct {
		Dependencies []map[string]json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(response.Body), &encoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := encoded.Dependencies[1]["version"]; exists {
		t.Fatal("dependency without a version advertised an empty version field")
	}
	legacyFields := decodeResponse[map[string]json.RawMessage](t, server.Request(t, http.MethodGet, "/api/v1/agents/"+legacy.ID, hostfixtures.Authenticate))
	if _, exists := legacyFields["dependencies"]; exists {
		t.Fatal("agent without declarations advertised dependency metadata")
	}
	record.Actions[0].Status, record.Actions[0].MissingRequirements = actions.Ready, nil
	record.ObservedAt = record.ObservedAt.Add(time.Millisecond)
	record.LastHeartbeat, record.LastSeen = record.ObservedAt, record.ObservedAt
	if err := inventory.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}

	ready := decodeResponse[models.AgentResponse](t, server.Request(t, http.MethodGet, path, hostfixtures.Authenticate))

	if len(ready.Actions) != 1 || ready.Actions[0].Status != "ready" || len(ready.Actions[0].MissingRequirements) != 0 || !slices.Equal(ready.Dependencies, detail.Dependencies) {
		t.Fatalf("readiness update changed static installation guidance: %+v", ready)
	}
	document := decodeResponse[struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}](t, server.Request(t, http.MethodGet, "/openapi.json", hostfixtures.Authenticate))
	schema := document.Components.Schemas["Agent"]
	if _, exists := schema.Properties["dependencies"]; !exists || slices.Contains(schema.Required, "dependencies") {
		t.Fatal("OpenAPI did not describe dependency metadata as optional")
	}
	dependencySchema := document.Components.Schemas["AgentDependency"]
	if !slices.Contains(dependencySchema.Required, "name") || !slices.Contains(dependencySchema.Required, "description") ||
		slices.Contains(dependencySchema.Required, "version") {
		t.Fatal("OpenAPI did not require dependency identity and guidance while keeping version optional")
	}
	if _, exists := dependencySchema.Properties["version"]; !exists {
		t.Fatal("OpenAPI omitted the optional dependency version field")
	}
}
