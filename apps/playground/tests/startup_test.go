package playground_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/playground/internal/local"
	bridge "github.com/defos-ai/commander/apps/playground/internal/mcp"
	fixtures "github.com/defos-ai/commander/apps/playground/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func testAllInOneDefaultAgents(t *testing.T) {
	fixtures.AllInOneEnvironment(t)
	t.Setenv("PATH", t.TempDir()) // Starting the built-in hosts must not require Git.
	session, _ := fixtures.StartLocalAdminSession(t, local.Options{})
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
	if session.AgentCount != 1 || len(inventory.Agents) != 1 || inventory.Agents[0].Type != "spawner-agent" {
		t.Fatalf("default all-in-one must start only the spawner: %+v", inventory.Agents)
	}
	spawner := inventory.Agents[0]
	if spawner.ID == "" || spawner.Status != agents.AgentReady || spawner.Stale || len(spawner.Actions) != 1 || spawner.Actions[0].Name != "spawn-agent" || spawner.Actions[0].Status != actions.Ready {
		t.Fatalf("default spawner is not ready to install agents: %+v", spawner)
	}
	awaitSpawnerCatalog(t, session.Context, client)
	var mission missions.Mission
	call("describe_mission", map[string]any{"mission_name": "install-agent"}, &mission)
	if !slices.Equal(mission.RequiredActions, []string{"spawn-agent"}) || !slices.Equal(mission.StartingActions, []string{"spawn-agent"}) {
		t.Fatalf("installation mission needs an agent other than the spawner: %+v", mission)
	}
	var provision operations.Operation
	call("start_operation", map[string]any{"mission_name": mission.Name, "input": map[string]any{"package": "github.com/defos-ai/commander-example-agent@v0.2.0"}}, &provision)
	if provision.Preflight == nil || !provision.Preflight.Ready || len(provision.Preflight.Actions) != 1 || provision.Preflight.Actions[0].ActionName != "spawn-agent" || !slices.Equal(provision.Preflight.Actions[0].ReadyAgentIDs, []string{spawner.ID}) || len(provision.Tasks) != 0 {
		t.Fatalf("installation cannot start with the default agents: %+v", provision)
	}
	call("cancel_operation", map[string]any{"operation_id": provision.ID}, nil)
	call("describe_mission", map[string]any{"mission_name": "build-agent"}, &mission)
	if !slices.Equal(mission.RequiredActions, []string{"checkout-template", "build-agent"}) || !slices.Equal(mission.StartingActions, []string{"checkout-template"}) {
		t.Fatalf("on-demand builder mission was not retained: %+v", mission)
	}
	var build operations.Operation
	call("start_operation", map[string]any{"mission_name": mission.Name, "input": map[string]any{
		"instructions": "Build the requested action after installing its builder.",
		"actions":      []map[string]any{{"name": "inspect-text", "description": "Inspect supplied text.", "input_schema": map[string]any{"type": "object"}, "output_schema": map[string]any{"type": "object"}}},
	}}, &build)
	if build.Preflight == nil || build.Preflight.Ready || len(build.Preflight.Actions) != 2 || len(build.Tasks) != 0 {
		t.Fatalf("build mission must wait for an installed builder: %+v", build)
	}
	for _, action := range build.Preflight.Actions {
		if len(action.ReadyAgentIDs) != 0 {
			t.Fatalf("uninstalled builder unexpectedly satisfies build preflight: %+v", action)
		}
	}
	call("cancel_operation", map[string]any{"operation_id": build.ID}, nil)
	for _, directory := range []string{"downloads", "agents"} {
		entries, err := os.ReadDir(filepath.Join(session.Directory, "spawner", directory))
		if err != nil || len(entries) != 0 {
			t.Fatalf("default startup downloaded or launched an agent: %s: %v (%v)", directory, entries, err)
		}
	}
}

func awaitSpawnerCatalog(t *testing.T, parent context.Context, client *bridge.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		request, cancelRequest := context.WithTimeout(ctx, time.Second)
		result, err := client.Call(request, "list_actions", json.RawMessage(`{}`))
		cancelRequest()
		if err != nil || result.IsError {
			t.Fatalf("MCP list_actions: %v (%s)", err, result.Text)
		}
		var catalog struct {
			Actions []struct{ Name string } `json:"actions"`
		}
		if err := json.Unmarshal(result.Data, &catalog); err != nil {
			t.Fatalf("decode list_actions: %v", err)
		}
		if len(catalog.Actions) == 1 && catalog.Actions[0].Name == "spawn-agent" {
			return
		}
		if len(catalog.Actions) != 0 {
			t.Fatalf("default action catalog must contain only spawn-agent, without builder actions: %+v", catalog.Actions)
		}
		// Inventory can become visible before its action catalog write commits.
		select {
		case <-ctx.Done():
			t.Fatal("spawner action did not appear in the default catalog before the deadline")
		case <-tick.C:
		}
	}
}
