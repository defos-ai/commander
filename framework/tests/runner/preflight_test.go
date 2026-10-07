package runner_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
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
		Actions:            []actions.Action{{Name: "inspect", Status: actions.Ready, RequiredPermissions: []string{"network-access"}}},
		GrantedPermissions: []string{"network-access"},
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
		{name: "missing configuration", requirement: actions.Requirement{Kind: actions.ConfigurationRequirement, Name: "SERVICE_API_KEY", Secret: true}},
		{name: "missing permission", change: func(a *agents.Agent) { a.GrantedPermissions = nil }, requirement: actions.Requirement{Kind: actions.PermissionRequirement, Name: "network-access"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := fixture()
			config.MissionStore.(*missionStore).mission.RequiredActions = []string{"inspect"}
			candidate := ready.Clone()
			if test.requirement.Kind != "" && test.requirement.Kind != actions.PermissionRequirement {
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

func TestPreflightPersistsAcrossRestartAndRediscoveryResumesQueuedWork(t *testing.T) {
	config := fixture()
	config.MissionStore.(*missionStore).mission.RequiredActions = []string{"inspect", "capture"}
	record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: agentID, Status: agents.AgentReady,
		Actions: []actions.Action{
			{Name: "inspect", Status: actions.Ready},
			{Name: "capture", Status: actions.Unavailable, MissingRequirements: []actions.Requirement{{Kind: actions.ExecutableRequirement, Name: "browser"}}},
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
