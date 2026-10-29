package runner_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func TestTaskRequirementsRequireCurrentExactExecution(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	deadline := now.Add(time.Minute)
	task := operations.NewTask(id.OperationID(), "", actions.NextAction{ActionName: "build-agent"})
	task.AgentID, task.Status, task.DeadlineAt = "builder-agent-12345678", operations.TaskRunning, &deadline
	want := []actions.Requirement{{Kind: actions.SecretRequirement, Name: "GITHUB_TOKEN"}}
	record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{
		ID: task.AgentID, Type: "builder-agent", Status: agents.AgentWorking,
		Work: &agents.Work{OperationID: task.OperationID, TaskID: task.ID, CommandID: task.CommandID, ActionName: task.ActionName, MissingRequirements: want},
	}, LastHeartbeat: now, LeaseExpiresAt: deadline}
	if got := operations.AssessTaskRequirements(task, record, now); !reflect.DeepEqual(got, want) {
		t.Fatalf("exact execution did not expose its undeclared runtime requirement: %+v", got)
	}
	tests := []struct {
		name   string
		change func(*operations.Task, *agents.Agent)
	}{
		{"queued", func(t *operations.Task, _ *agents.Agent) { t.Status = operations.TaskQueued }},
		{"completed", func(t *operations.Task, _ *agents.Agent) { t.Status = operations.TaskSucceeded }},
		{"interrupted", func(t *operations.Task, _ *agents.Agent) { t.Status = operations.TaskInterrupted }},
		{"no deadline", func(t *operations.Task, _ *agents.Agent) { t.DeadlineAt = nil }},
		{"deadline elapsed", func(t *operations.Task, _ *agents.Agent) { t.DeadlineAt = &now }},
		{"wrong agent", func(_ *operations.Task, a *agents.Agent) { a.ID = "builder-agent-87654321" }},
		{"stale", func(_ *operations.Task, a *agents.Agent) { a.Stale = true }},
		{"lease elapsed", func(_ *operations.Task, a *agents.Agent) { a.LeaseExpiresAt = now }},
		{"no heartbeat", func(_ *operations.Task, a *agents.Agent) { a.LastHeartbeat = time.Time{} }},
		{"stopped", func(_ *operations.Task, a *agents.Agent) { a.Status = agents.AgentStopped }},
		{"idle", func(_ *operations.Task, a *agents.Agent) { a.Status = agents.AgentReady }},
		{"invalid type", func(_ *operations.Task, a *agents.Agent) { a.Type = "invalid type" }},
		{"different registered type", func(_ *operations.Task, a *agents.Agent) { a.Type = "other-agent" }},
		{"no work", func(_ *operations.Task, a *agents.Agent) { a.Work = nil }},
		{"different operation", func(_ *operations.Task, a *agents.Agent) { a.Work.OperationID = id.OperationID() }},
		{"different task", func(_ *operations.Task, a *agents.Agent) { a.Work.TaskID = id.TaskID() }},
		{"different command", func(_ *operations.Task, a *agents.Agent) { a.Work.CommandID = id.TaskID() }},
		{"different action", func(_ *operations.Task, a *agents.Agent) { a.Work.ActionName = "other" }},
		{"missing identity", func(t *operations.Task, a *agents.Agent) { t.CommandID, a.Work.CommandID = "", "" }},
		{"invalid name", func(_ *operations.Task, a *agents.Agent) { a.Work.MissingRequirements[0].Name = "not a name" }},
		{"host configuration", func(_ *operations.Task, a *agents.Agent) {
			a.Work.MissingRequirements[0].Kind = actions.ConfigurationRequirement
		}},
		{"invalid sensitivity", func(_ *operations.Task, a *agents.Agent) { a.Work.MissingRequirements[0].Sensitive = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copyTask, copyAgent := task.Clone(), record.Clone()
			test.change(&copyTask, &copyAgent)
			if got := operations.AssessTaskRequirements(copyTask, copyAgent, now); len(got) != 0 {
				t.Fatalf("unrelated or stale evidence became a task wait: %+v", got)
			}
		})
	}
	got := operations.AssessTaskRequirements(task, record, now)
	got[0].Name = "CHANGED"
	if !reflect.DeepEqual(record.Work.MissingRequirements, want) {
		t.Fatal("projected requirements share mutable inventory storage")
	}
}
