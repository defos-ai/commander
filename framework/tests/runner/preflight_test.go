package runner_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type agentCatalog struct {
	records atomic.Value
}

func (c *agentCatalog) Agents(context.Context) ([]agents.Agent, error) {
	records, _ := c.records.Load().([]agents.Agent)
	result := make([]agents.Agent, len(records))
	for i, record := range records {
		result[i] = record.Clone()
	}
	return result, nil
}

func (c *agentCatalog) set(records ...agents.Agent) {
	snapshot := make([]agents.Agent, len(records))
	for i, record := range records {
		snapshot[i] = record.Clone()
	}
	c.records.Store(snapshot)
}

func TestPreflightChecksMissionCoverageAcrossAgents(t *testing.T) {
	ready := agents.Agent{AgentSnapshot: agents.AgentSnapshot{
		ID: agentID, Status: agents.AgentReady,
		Actions: []actions.Action{{Name: "inspect", Status: actions.Ready}},
	}}
	tests := []struct {
		name        string
		change      func(*agents.Agent)
		noAgent     bool
		wantReady   bool
		requirement actions.Requirement
	}{
		{name: "ready", wantReady: true},
		{name: "busy is capacity rather than a missing dependency", wantReady: true, change: func(a *agents.Agent) { a.Status = agents.AgentWorking }},
		{name: "no implementing agent", noAgent: true},
		{name: "stale agent", change: func(a *agents.Agent) { a.Stale = true }},
		{name: "stopped agent", change: func(a *agents.Agent) { a.Status = agents.AgentStopped }},
		{name: "undiscovered action", change: func(a *agents.Agent) { a.Actions[0].Status = actions.Unknown }},
		{name: "missing executable", requirement: actions.Requirement{Kind: actions.ExecutableRequirement, Name: "browser"}},
		{name: "missing configuration", requirement: actions.Requirement{Kind: actions.ConfigurationRequirement, Name: "SERVICE_API_KEY", Sensitive: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := fixture()
			config.MissionStore.(*missionStore).mission.RequiredActions = []string{"inspect"}
			candidate := ready.Clone()
			if test.requirement.Kind != "" {
				candidate.Actions[0].Status = actions.Unavailable
				candidate.Actions[0].MissingRequirements = []actions.Requirement{test.requirement}
			}
			if test.change != nil {
				test.change(&candidate)
			}
			catalog := &agentCatalog{}
			if !test.noAgent {
				catalog.set(candidate)
			}
			config.AgentCatalog = catalog
			r := newRunner(t, config, executor{})

			op := start(t, r)

			if op.Preflight == nil || op.Preflight.Ready != test.wantReady || len(op.Preflight.Actions) != 1 || op.Preflight.Version != 1 {
				t.Fatalf("unexpected preflight: %+v", op.Preflight)
			}
			coverage := op.Preflight.Actions[0]
			if test.wantReady {
				if op.Status != operations.Planning || len(coverage.ReadyAgentIDs) != 1 || coverage.ReadyAgentIDs[0] != agentID {
					t.Fatalf("ready agent was not selected: %+v", op)
				}
			} else if op.Status != operations.Waiting || len(coverage.ReadyAgentIDs) != 0 {
				t.Fatalf("missing requirement did not block preflight: %+v", op)
			}
			if test.requirement.Kind != "" {
				if len(coverage.UnavailableAgents) != 1 || len(coverage.UnavailableAgents[0].MissingRequirements) != 1 || coverage.UnavailableAgents[0].MissingRequirements[0] != test.requirement {
					t.Fatalf("missing structured requirement: %+v", coverage)
				}
			}
		})
	}

	t.Run("different agents provide different actions", func(t *testing.T) {
		config := fixture()
		config.MissionStore.(*missionStore).mission.RequiredActions = []string{"inspect", "capture"}
		browser := ready.Clone()
		browser.ID = "review-agent-00000002"
		browser.Actions = []actions.Action{{Name: "capture", Status: actions.Ready}}
		catalog := &agentCatalog{}
		catalog.set(ready, browser)
		config.AgentCatalog = catalog
		r := newRunner(t, config, executor{})

		op := start(t, r)

		if !op.Preflight.Ready || len(op.Preflight.Actions) != 2 || op.Preflight.Actions[0].ReadyAgentIDs[0] != browser.ID || op.Preflight.Actions[1].ReadyAgentIDs[0] != ready.ID {
			t.Fatalf("mission incorrectly required one fully equipped agent: %+v", op.Preflight)
		}
	})
}

func TestOptionalActionChecksReadinessOnlyWhenQueued(t *testing.T) {
	config := fixture()
	definition := &config.MissionStore.(*missionStore).mission
	definition.RequiredActions = []string{"inspect"}
	definition.OptionalActions = []string{"capture"}
	record := readyAgent(agentID)
	record.Actions = []actions.Action{{Name: "inspect", Status: actions.Ready}, {Name: "capture", Status: actions.Unavailable,
		RequiredSecrets:     []string{"BROWSER_TOKEN"},
		MissingRequirements: []actions.Requirement{{Kind: actions.SecretRequirement, Name: "BROWSER_TOKEN"}},
	}}
	catalog := &agentCatalog{}
	catalog.set(record)
	config.AgentCatalog = catalog
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	if !op.Preflight.Ready || len(op.Preflight.Actions) != 1 || !slices.Equal(op.Mission.OptionalActions, []string{"capture"}) {
		t.Fatalf("unscheduled optional action blocked initial preflight or was lost: %+v", op)
	}
	task := assign(t, r, op.ID, "capture")
	closeRunner(t, r)
	r = newRunner(t, config, exec)
	blocked := awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return op.Preflight != nil && !op.Preflight.Ready && len(op.Preflight.Actions) == 2
	})
	coverage := blocked.Preflight.Actions[0]
	if blocked.Status != operations.Waiting || blocked.Tasks[0].Status != operations.TaskQueued || coverage.ActionName != "capture" || len(coverage.UnavailableAgents) != 1 || !slices.Equal(coverage.UnavailableAgents[0].MissingRequirements, record.Actions[1].MissingRequirements) {
		t.Fatalf("queued optional action lost its secret requirement across restart: %+v", blocked)
	}
	select {
	case call := <-calls:
		t.Fatalf("optional action dispatched without its secret: %+v", call)
	default:
	}
	record.Actions[1].Status = actions.Ready
	record.Actions[1].MissingRequirements = nil
	catalog.set(record)
	call := receive(t, calls)
	if call.call.ActionName != "capture" {
		t.Fatalf("wrong optional action dispatched: %+v", call)
	}
	call.reply <- output("capture")
	finished := awaitOperation(t, r, op.ID, func(op operations.Operation) bool {
		return op.Tasks[0].ID == task.ID && op.Tasks[0].Status == operations.TaskSucceeded && op.Preflight.Ready && len(op.Preflight.Actions) == 1
	})
	if !slices.Equal(finished.Mission.OptionalActions, []string{"capture"}) {
		t.Fatal("execution changed the mission's optional actions")
	}
}

func TestPreflightPersistsAcrossRestartAndRediscoveryResumesQueuedWork(t *testing.T) {
	config := fixture()
	config.MissionStore.(*missionStore).mission.RequiredActions = []string{"inspect", "capture"}
	record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: agentID, Type: "browser", Status: agents.AgentReady,
		Actions: []actions.Action{
			{Name: "inspect", Status: actions.Ready},
			{Name: "capture", Status: actions.Unavailable, RequiredSecrets: []string{"BROWSER_TOKEN"}, MissingRequirements: []actions.Requirement{{Kind: actions.SecretRequirement, Name: "BROWSER_TOKEN"}}},
		},
	}}
	catalog := &agentCatalog{}
	catalog.set(record)
	config.AgentCatalog = catalog
	exec, calls := controlledExecutor()
	r := newRunner(t, config, exec)
	op := start(t, r)
	task := assign(t, r, op.ID, "inspect")
	closeRunner(t, r)
	r = newRunner(t, config, exec)
	restored, err := r.Get(t.Context(), op.ID)
	if err != nil || restored.Status != operations.Waiting || restored.Preflight.Ready || restored.Tasks[0].Status != operations.TaskQueued {
		t.Fatalf("restart lost preflight or dispatched blocked work: %+v, %v", restored, err)
	}
	candidate := restored.Preflight.Actions[0].UnavailableAgents[0]
	wantMissing := []actions.Requirement{{Kind: actions.SecretRequirement, Name: "BROWSER_TOKEN"}}
	if candidate.AgentType != "browser" || !slices.Equal(candidate.MissingRequirements, wantMissing) {
		t.Fatalf("restart lost managed secret requirement: %+v", candidate)
	}
	candidate.MissingRequirements[0].Name = "changed snapshot"
	saved, err := r.Get(t.Context(), op.ID)
	if err != nil || !slices.Equal(saved.Preflight.Actions[0].UnavailableAgents[0].MissingRequirements, wantMissing) {
		t.Fatalf("reading secret requirements changed saved preflight: %+v, %v", saved.Preflight, err)
	}

	// Changing the gap proves the scheduler processed this inventory while blocked.
	record.Actions[1].MissingRequirements[0] = actions.Requirement{Kind: actions.ConfigurationRequirement, Name: "BROWSER_PATH"}
	catalog.set(record)
	blocked := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Preflight.Version == 2 })
	select {
	case call := <-calls:
		t.Fatalf("work dispatched before mission preflight passed: %+v", call)
	default:
	}
	if blocked.Tasks[0].Status != operations.TaskQueued {
		t.Fatalf("task did not remain queued: %+v", blocked.Tasks[0])
	}
	if !slices.Equal(blocked.Preflight.Actions[0].UnavailableAgents[0].MissingRequirements, record.Actions[1].MissingRequirements) {
		t.Fatal("a nonsecret requirement retained the previous managed secret wait")
	}

	record.Actions[1].Status, record.Actions[1].MissingRequirements = actions.Ready, nil
	catalog.set(record)
	job := receive(t, calls)
	job.reply <- output("inspect")
	finished := awaitOperation(t, r, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })

	if !finished.Preflight.Ready || finished.Preflight.Version != 3 || len(finished.Tasks) != 1 || finished.Tasks[0].ID != task.ID || job.call.Work.TaskID != task.ID {
		t.Fatalf("rediscovery duplicated or failed to resume work: %+v", finished)
	}
	_, err = r.Complete(t.Context(), commands.CompleteOperationCommand{OperationID: op.ID, Output: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPreflightExposesCurrentRequirementsOnLiveCandidates(t *testing.T) {
	base := readyAgent(agentID)
	base.Type = "builder"
	base.Actions[0].Status = actions.Unavailable
	base.Actions[0].RequiredSecrets = []string{"TOKEN", "SECOND_TOKEN", "AVAILABLE_TOKEN"}
	base.Actions[0].MissingRequirements = []actions.Requirement{
		{Kind: actions.SecretRequirement, Name: "TOKEN"},
		{Kind: actions.SecretRequirement, Name: "SECOND_TOKEN"},
		{Kind: actions.ConfigurationRequirement, Name: "ENV_TOKEN", Sensitive: true},
		{Kind: actions.ConfigurationRequirement, Name: "PATH"},
		{Kind: actions.ExecutableRequirement, Name: "git"},
	}
	tests := []struct {
		name   string
		change func(*agents.Agent)
		live   bool
	}{
		{name: "live candidate", live: true},
		{name: "working candidate", change: func(a *agents.Agent) { a.Status = agents.AgentWorking }, live: true},
		{name: "sensitive environment only", change: func(a *agents.Agent) {
			a.Actions[0].RequiredSecrets = nil
			a.Actions[0].MissingRequirements = []actions.Requirement{{Kind: actions.ConfigurationRequirement, Name: "ENV_TOKEN", Sensitive: true}}
		}, live: true},
		{name: "declared but no longer missing", change: func(a *agents.Agent) {
			a.Actions[0].MissingRequirements = nil
		}, live: true},
		{name: "stale candidate", change: func(a *agents.Agent) { a.Stale = true }},
		{name: "stopped candidate", change: func(a *agents.Agent) { a.Status = agents.AgentStopped }},
		{name: "discovering candidate", change: func(a *agents.Agent) { a.Status = agents.AgentDiscovering }},
	}
	mission := missions.Mission{RequiredActions: []string{"inspect"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base.Clone()
			if test.change != nil {
				test.change(&candidate)
			}
			original := slices.Clone(candidate.Actions[0].MissingRequirements)
			preflight, err := (operations.Operation{Mission: mission}).AssessPreflight([]agents.Agent{candidate})
			if err != nil {
				t.Fatal(err)
			}
			if preflight.Ready || len(preflight.Actions) != 1 || len(preflight.Actions[0].UnavailableAgents) != 1 {
				t.Fatalf("missing requirements no longer block preflight: %+v", preflight)
			}
			got := preflight.Actions[0].UnavailableAgents[0]
			var want []actions.Requirement
			if test.live {
				want = original
			}
			if got.AgentType != string(candidate.Type) || !slices.Equal(got.MissingRequirements, want) {
				t.Fatalf("current requirements: got %+v; want type %q, requirements %v", got, candidate.Type, want)
			}
			if len(got.MissingRequirements) > 0 {
				got.MissingRequirements[0].Name = "changed snapshot"
			}
			if !slices.Equal(candidate.Actions[0].MissingRequirements, original) {
				t.Fatal("preflight changed the agent's last reported requirements")
			}
		})
	}

	t.Run("another ready agent covers the action", func(t *testing.T) {
		ready := readyAgent("review-agent-00000002")
		preflight, err := (operations.Operation{Mission: mission}).AssessPreflight([]agents.Agent{base, ready})
		if err != nil {
			t.Fatal(err)
		}
		if !preflight.Ready || len(preflight.Actions[0].UnavailableAgents) != 0 || !slices.Equal(preflight.Actions[0].ReadyAgentIDs, []string{ready.ID}) {
			t.Fatalf("an available candidate did not clear the action's wait: %+v", preflight)
		}
	})
}
