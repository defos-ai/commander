package playground_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/local"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func testAllInOneDefaultAgents(t *testing.T) {
	allInOneEnvironment(t)
	t.Setenv("PATH", t.TempDir()) // Starting the built-in hosts must not require Git.
	session, _ := startLocalAdminSession(t, local.Options{DisableAdmin: true})
	client, err := bridge.Connect(session.Context, session.MCPURL, session.MCPCredentials)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call := func(name string, arguments map[string]any, target any) {
		t.Helper()
		encoded, err := json.Marshal(arguments)
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Call(session.Context, name, encoded)
		if err != nil || result.IsError {
			t.Fatalf("MCP %s: %v (%s)", name, err, result.Text)
		}
		if target != nil {
			if err := json.Unmarshal(result.Data, target); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
		}
	}
	var inventory struct {
		Agents []agents.Agent `json:"agents"`
	}
	call("list_agents", map[string]any{}, &inventory)
	if session.AgentCount != 2 || len(inventory.Agents) != 2 {
		t.Fatalf("default all-in-one did not start builder and spawner: %+v", inventory.Agents)
	}
	var builder, spawner agents.Agent
	for _, agent := range inventory.Agents {
		switch agent.Type {
		case "builder-agent":
			builder = agent
		case "spawner-agent":
			spawner = agent
		}
	}
	if spawner.ID == "" || spawner.Status != agents.AgentReady || spawner.Stale || len(spawner.Actions) != 1 || spawner.Actions[0].Name != "spawn-agent" || spawner.Actions[0].Status != actions.Ready {
		t.Fatalf("default spawner is not ready to install agents: %+v", spawner)
	}
	if builder.ID == "" || builder.Status != agents.AgentReady || builder.Stale || len(builder.Actions) != 1 || builder.Actions[0].Name != "checkout-template" || builder.Actions[0].Status != actions.Unavailable ||
		!slices.Equal(builder.Actions[0].MissingRequirements, []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "git"}}) {
		t.Fatalf("default builder did not report its missing Git dependency: %+v", builder)
	}
	var mission missions.Mission
	call("describe_mission", map[string]any{"mission_name": "install-agent"}, &mission)
	if !slices.Equal(mission.RequiredActions, []string{"spawn-agent"}) || !slices.Equal(mission.StartingActions, []string{"spawn-agent"}) {
		t.Fatalf("installation mission needs an agent other than the spawner: %+v", mission)
	}
	var provision operations.Operation
	call("start_operation", map[string]any{"mission_name": mission.Name, "input": map[string]any{"package": "github.com/defos-ai/commander-web-agent@v0.2.0"}}, &provision)
	if provision.Preflight == nil || !provision.Preflight.Ready || len(provision.Preflight.Actions) != 1 || provision.Preflight.Actions[0].ActionName != "spawn-agent" || !slices.Equal(provision.Preflight.Actions[0].ReadyAgentIDs, []string{spawner.ID}) || len(provision.Tasks) != 0 {
		t.Fatalf("installation cannot start with the default agents: %+v", provision)
	}
	call("cancel_operation", map[string]any{"operation_id": provision.ID}, nil)
	for _, directory := range []string{"downloads", "records", "agents"} {
		entries, err := os.ReadDir(filepath.Join(session.Directory, "spawner", directory))
		if err != nil || len(entries) != 0 {
			t.Fatalf("default startup downloaded or launched an agent: %s: %v (%v)", directory, entries, err)
		}
	}
}
