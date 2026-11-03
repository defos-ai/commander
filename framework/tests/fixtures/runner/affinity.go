package fixtures

import (
	"time"

	"github.com/defos-ai/commander/framework/controller/runner"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/runner"
)

func AffinityOperation() operations.Operation {
	op := operations.Operation{
		ID: id.OperationID(), Status: operations.Planning,
		Mission: missions.Mission{Policies: &policies.MissionPolicies{
			Placement: &policies.PlacementPolicy{SameAgent: []string{"checkout-template", "build-agent"}},
		}},
	}
	for _, name := range []string{"checkout-template", "build-agent", "other-action", "another-action"} {
		op.Tasks = append(op.Tasks, operations.NewTask(op.ID, "", actions.NextAction{ActionName: name}))
	}
	return op
}

func AffinityDispatch(task operations.Task, agentID string) operations.TaskDispatch {
	start := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC)
	return operations.TaskDispatch{
		TaskID: task.ID, AgentID: agentID, StartedAt: start, DeadlineAt: start.Add(time.Minute),
	}
}

func AffinityPreflightAgent(agentID string, names ...string) agents.Agent {
	record := agents.Agent{AgentSnapshot: agents.AgentSnapshot{ID: agentID, Type: "builder-agent", Status: agents.AgentReady}}
	for _, name := range names {
		record.Actions = append(record.Actions, actions.Action{Name: name, Status: actions.Ready})
	}
	return record
}

func AffinityConfig() runner.Config {
	config := Config()
	config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
		m.Policies = &policies.MissionPolicies{Placement: AffinityPolicy()}
	})
	return config
}

func AffinityPolicy() *policies.PlacementPolicy {
	return &policies.PlacementPolicy{SameAgent: []string{"inspect", "discover"}}
}
