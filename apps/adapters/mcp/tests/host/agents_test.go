package host_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/mcp/tests/fixtures"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func TestAgentDetailsRetainVersionAndDependenciesAcrossReadinessUpdates(t *testing.T) {
	a, inventory := fixtures.HostWithInventory(t)
	announced := fixtures.AgentWithDependencies()
	if err := inventory.Upsert(a.Context, announced); err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage

	a.Call("describe_agent", fixtures.Object{"agent_id": announced.ID}, &raw)

	var detail agents.Agent
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Version != announced.Version || !slices.Equal(detail.Dependencies, announced.Dependencies) || detail.Stale || len(detail.Actions) != 2 ||
		detail.Actions[0].Status != actions.Unavailable || len(detail.Actions[0].MissingRequirements) != 1 || detail.Actions[1].Status != actions.Ready {
		t.Fatalf("agent details changed package version, dependencies or readiness: %+v", detail)
	}
	if dependency, found := detail.DependencyFor(detail.Actions[0].MissingRequirements[0]); !found || dependency != announced.Dependencies[0] {
		t.Fatalf("missing executable lost its installation guidance: %+v found=%v", dependency, found)
	}
	var fields struct {
		Dependencies []map[string]json.RawMessage `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields.Dependencies[1]["version"]; present {
		t.Fatal("agent detail invented a version for an unversioned declaration")
	}
	var listed struct {
		Agents []map[string]json.RawMessage `json:"agents"`
	}

	a.Call("list_agents", fixtures.Object{}, &listed)

	if len(listed.Agents) != 1 {
		t.Fatalf("agent list lost saved inventory: %+v", listed)
	}
	var listedVersion string
	if err := json.Unmarshal(listed.Agents[0]["version"], &listedVersion); err != nil || listedVersion != announced.Version {
		t.Fatalf("agent list changed the reported package version: got %q, want %q, error=%v", listedVersion, announced.Version, err)
	}
	if _, present := listed.Agents[0]["dependencies"]; present {
		t.Fatal("compact agent list included dependency installation metadata")
	}
	var listedActions []actions.Action
	if err := json.Unmarshal(listed.Agents[0]["actions"], &listedActions); err != nil {
		t.Fatal(err)
	}
	if len(listedActions) != 2 || listedActions[0].Status != actions.Unavailable || len(listedActions[0].MissingRequirements) != 1 {
		t.Fatalf("compact agent list lost missing executable requirements: %+v", listedActions)
	}
	announced.Actions[0].Status = actions.Ready
	announced.Actions[0].StatusReason = ""
	announced.Actions[0].MissingRequirements = nil
	announced.ObservedAt = announced.ObservedAt.Add(time.Nanosecond)
	if err := inventory.Upsert(a.Context, announced); err != nil {
		t.Fatal(err)
	}
	var ready agents.Agent

	a.Call("describe_agent", fixtures.Object{"agent_id": announced.ID}, &ready)

	if ready.Version != announced.Version || len(ready.Actions) != 2 || ready.Actions[0].Status != actions.Ready || len(ready.Actions[0].MissingRequirements) != 0 ||
		!slices.Equal(ready.Dependencies, announced.Dependencies) {
		t.Fatalf("readiness update changed package metadata or retained missing requirements: %+v", ready)
	}
	announced.Dependencies = nil
	announced.Version = ""
	announced.ObservedAt = announced.ObservedAt.Add(time.Nanosecond)
	if err := inventory.Upsert(a.Context, announced); err != nil {
		t.Fatal(err)
	}
	var withoutDeclarations map[string]json.RawMessage

	a.Call("describe_agent", fixtures.Object{"agent_id": announced.ID}, &withoutDeclarations)

	if _, present := withoutDeclarations["dependencies"]; present {
		t.Fatal("agent details retained omitted dependency declarations")
	}
	if _, present := withoutDeclarations["version"]; present {
		t.Fatal("agent details invented an unreported package version")
	}
	var unversionedList struct {
		Agents []map[string]json.RawMessage `json:"agents"`
	}

	a.Call("list_agents", fixtures.Object{}, &unversionedList)

	if len(unversionedList.Agents) != 1 {
		t.Fatalf("agent list lost the unversioned agent: %+v", unversionedList)
	}
	if _, present := unversionedList.Agents[0]["version"]; present {
		t.Fatal("agent list invented an unreported package version")
	}
}
