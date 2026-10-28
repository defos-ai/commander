package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fixtures"
)

func TestAgentCatalogKeepsRegisteredIdentity(t *testing.T) {
	a := fixtures.Connect(t)
	initial := fixtures.Read[models.ListResponse[models.AgentResponse]](a, "/api/v1/agents")
	if !slices.IsSortedFunc(initial.Items, func(left, right models.AgentResponse) int { return strings.Compare(left.ID, right.ID) }) {
		t.Fatalf("agent inventory is not sorted: %+v", initial)
	}
	for _, agent := range initial.Items {
		result := a.Request(http.MethodGet, "/api/v1/agents/"+agent.ID, nil)
		detail := fixtures.Decode[models.AgentResponse](t, result, http.StatusOK)
		if detail.ID != agent.ID || detail.Stale || detail.Work != nil || detail.Status != "ready" {
			t.Fatalf("agent is not ready: %+v", detail)
		}
		assertSchema(t, a, "Agent", result)
	}
	fixtures.AssertError(t, a.Request(http.MethodGet, "/api/v1/agents/not-valid", nil), http.StatusNotFound, "agent_not_found")
	fixtures.AssertError(t, a.Request(http.MethodGet, "/api/v1/agents/unknown-agent-1234abcd", nil), http.StatusNotFound, "agent_not_found")
}
