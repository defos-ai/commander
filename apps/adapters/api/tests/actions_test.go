package api_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
)

func TestActionCatalogMatchesReadyAgents(t *testing.T) {
	a := connect(t)
	catalog := read[models.ListResponse[models.ActionResponse]](a, "/api/v1/actions")
	agents := read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents")
	if !slices.Equal(actionNames(catalog.Items), expectedCatalogActions(agents.Items)) {
		t.Fatalf("catalog differs from the registered agents' capabilities: %+v", catalog)
	}
	for i, action := range catalog.Items {
		if i > 0 && catalog.Items[i-1].Name >= action.Name {
			t.Fatal("action catalog is not sorted by name")
		}
		if !json.Valid(action.InputSchema) || !json.Valid(action.OutputSchema) || action.Description == "" {
			t.Fatalf("action lacks definition: %+v", action)
		}
		path := "/api/v1/actions/" + action.Name
		if detail := read[models.ActionResponse](a, path); !reflect.DeepEqual(detail, action) {
			t.Fatalf("action detail differs from catalog: %+v", detail)
		}
		assertSchema(t, a, "Action", a.request(http.MethodGet, path, nil))
	}
	for _, agent := range agents.Items {
		var names []string
		for _, action := range agent.Actions {
			names = append(names, action.Name)
			if action.Name == "build-agent" || action.Name == "publish-agent" {
				secret := "CODEX_API_KEY"
				if action.Name == "publish-agent" {
					secret = "GITHUB_TOKEN"
				}
				if action.Status != "unavailable" || !slices.Equal(action.RequiredSecrets, []string{secret}) || len(action.MissingRequirements) != 1 || action.MissingRequirements[0].Kind != "secret" || action.MissingRequirements[0].Name != secret {
					t.Fatalf("builder tools must be installed while its managed key remains missing: %+v", action)
				}
			} else if action.Status != "ready" {
				t.Fatalf("Compose must install browser/dependencies: %s: %+v", agent.ID, action)
			}
		}
		slices.Sort(names)
		if !slices.Equal(names, expectedAgentActions(agent.Type)) {
			t.Fatalf("agent advertises the wrong capabilities: %+v", agent)
		}
	}
	assertError(t, a.request(http.MethodGet, "/api/v1/actions/missing", nil), http.StatusNotFound, "action_not_found")
}
