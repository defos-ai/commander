package api_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/mappings"
	"github.com/defos-ai/commander/apps/adapters/api/models"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	actionfs "github.com/defos-ai/commander/providers/fs/actions"
	agentfs "github.com/defos-ai/commander/providers/fs/agents"
)

func TestCatalogQueriesPreserveSecretDeclarations(t *testing.T) {
	fixture := hostfixtures.New(t)
	database := filepath.Join(fixture.Root, "database")
	actionStore, err := actionfs.NewActionFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	agentStore, err := agentfs.NewAgentFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	action := actions.Action{
		Name: "build-agent", Status: actions.Unknown, RequiredSecrets: []string{"MODEL_API_KEY", "GITHUB_TOKEN"},
		InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
	}
	if err := actionStore.Upsert(t.Context(), action); err != nil {
		t.Fatal(err)
	}
	advertised := action
	advertised.Status = actions.Ready
	now := time.Now().UTC()
	record := agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: "builder-agent-12345678", Type: "builder-agent",
			Status: agents.AgentReady, Actions: []actions.Action{advertised}},
		ObservedAt: now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Hour),
	}
	if err := agentStore.Upsert(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	server := hostfixtures.Start(t, fixture.LocalArgs())
	list := decodeResponse[models.ListResponse[models.ActionResponse]](t, server.Request(t, http.MethodGet, "/api/v1/actions", hostfixtures.Authenticate))
	detail := decodeResponse[models.ActionResponse](t, server.Request(t, http.MethodGet, "/api/v1/actions/"+action.Name, hostfixtures.Authenticate))
	if len(list.Items) != 1 || !reflect.DeepEqual(list.Items[0], detail) || !reflect.DeepEqual(detail.RequiredSecrets, action.RequiredSecrets) {
		t.Fatalf("action queries lost secret declarations: list=%+v detail=%+v", list, detail)
	}
	agentList := decodeResponse[models.ListResponse[models.AgentResponse]](t, server.Request(t, http.MethodGet, "/api/v1/agents", hostfixtures.Authenticate))
	agentDetail := decodeResponse[models.AgentResponse](t, server.Request(t, http.MethodGet, "/api/v1/agents/"+record.ID, hostfixtures.Authenticate))
	if len(agentList.Items) != 1 || !reflect.DeepEqual(agentList.Items[0], agentDetail) ||
		len(agentDetail.Actions) != 1 || !reflect.DeepEqual(agentDetail.Actions[0].RequiredSecrets, action.RequiredSecrets) || agentDetail.Actions[0].Status != "ready" {
		t.Fatalf("agent queries lost ready-action requirements: list=%+v detail=%+v", agentList, agentDetail)
	}
}

func TestSecretDeclarationMappingsDoNotAliasSource(t *testing.T) {
	action := actions.Action{Name: "build-agent", RequiredSecrets: []string{"GITHUB_TOKEN"}}
	record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{Actions: []actions.Action{action}}}
	response := mappings.ToActionResponse(action)
	agentResponse := mappings.ToAgentResponse(record)
	response.RequiredSecrets[0] = "changed"
	agentResponse.Actions[0].RequiredSecrets[0] = "changed"
	if action.RequiredSecrets[0] != "GITHUB_TOKEN" || record.Actions[0].RequiredSecrets[0] != "GITHUB_TOKEN" {
		t.Fatal("API response mutation changed source declarations")
	}
	preflight := operations.Preflight{Actions: []operations.ActionReadiness{{
		ActionName: action.Name, UnavailableAgents: []operations.AgentReadiness{{
			AgentID: "builder-agent-12345678", AgentType: "builder-agent",
			MissingRequirements: []actions.Requirement{{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"}},
		}},
	}}}
	mapped := mappings.ToPreflightResponse(preflight)
	if mapped.Actions[0].UnavailableAgents[0].AgentType != "builder-agent" {
		t.Fatal("API preflight lost the shared secret name or requesting agent type")
	}
	if mapped.Actions[0].UnavailableAgents[0].MissingRequirements[0].Kind != "secret" {
		t.Fatal("API preflight lost the managed secret kind")
	}
	mapped.Actions[0].UnavailableAgents[0].MissingRequirements[0].Name = "changed"
	if preflight.Actions[0].UnavailableAgents[0].MissingRequirements[0].Name != "GITHUB_TOKEN" {
		t.Fatal("API response mutation changed source secret requirements")
	}
}
