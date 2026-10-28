package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/adapters/api/models"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/id"
)

func TestSessionDiscoveryValidatesQueriesAndDocumentsLookup(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, append(fixture.LocalArgs(), "--swagger"))
	missing := id.New()
	for _, query := range []string{"", "?scope_id=" + missing, "?owner_agent_id=fixture-agent-00000001", "?scope_id=" + missing + "&owner_agent_id=fixture-agent-00000001"} {
		result := decodeResponse[models.ListResponse[models.RuntimeSessionResponse]](t, server.Request(t, http.MethodGet, "/api/v1/sessions"+query, hostfixtures.Authenticate))
		if result.Items == nil || len(result.Items) != 0 {
			t.Fatalf("empty discovery did not retain an empty items array: %+v", result)
		}
	}
	for _, tc := range []struct{ path, code string }{
		{"?unknown=1", "invalid_query"}, {"?scope_id=", "invalid_query"}, {"?owner_agent_id=", "invalid_query"},
		{"?scope_id=" + missing + "&scope_id=" + missing, "invalid_query"},
		{"?owner_agent_id=fixture-agent-00000001&owner_agent_id=fixture-agent-00000001", "invalid_query"},
		{"?scope_id=invalid", "invalid_scope_id"}, {"?owner_agent_id=fixture", "invalid_owner_agent_id"},
		{"?owner_agent_id=fixture-agent-ABCDEF12", "invalid_owner_agent_id"}, {"?scope_id=%zz", "invalid_query"},
		{"/invalid", "invalid_session_id"}, {"/00000000-0000-4000-8000-000000ABCDEF", "invalid_session_id"},
		{"/" + missing + "?scope_id=" + missing, "invalid_query"},
	} {
		result := decodeResponse[struct{ Error string }](t, server.Request(t, http.MethodGet, "/api/v1/sessions"+tc.path, hostfixtures.Authenticate), http.StatusBadRequest)
		if result.Error != tc.code {
			t.Errorf("%s: got %q, want %q", tc.path, result.Error, tc.code)
		}
	}
	absent := decodeResponse[struct{ Error string }](t, server.Request(t, http.MethodGet, "/api/v1/sessions/"+missing, hostfixtures.Authenticate), http.StatusNotFound)
	if absent.Error != "session_not_found" {
		t.Fatalf("missing ID lookup: %+v", absent)
	}
	document := decodeResponse[struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name     string
				In       string
				Required bool
			}
			Responses map[string]struct{ Description string }
		}
	}](t, server.Request(t, http.MethodGet, "/openapi.json", hostfixtures.Authenticate))
	list := document.Paths["/api/v1/sessions"]["get"]
	if len(list.Parameters) != 2 || list.Parameters[0].Name != "scope_id" || list.Parameters[1].Name != "owner_agent_id" {
		t.Fatalf("session discovery parameters: %+v", list.Parameters)
	}
	for _, parameter := range list.Parameters {
		if parameter.Required || parameter.In != "query" {
			t.Fatalf("session filter must be optional: %+v", parameter)
		}
	}
	get := document.Paths["/api/v1/sessions/{session_id}"]["get"]
	if get.OperationID != "getRuntimeSession" || len(get.Parameters) != 1 || get.Parameters[0].Name != "session_id" || get.Parameters[0].In != "path" || !get.Parameters[0].Required || !strings.Contains(get.Responses["404"].Description, "session_not_found") {
		t.Fatalf("session ID lookup contract: %+v", get)
	}
}
