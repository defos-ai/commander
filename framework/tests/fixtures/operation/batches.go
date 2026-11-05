package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/id"
)

func AggregationReview(t *testing.T) operations.Operation {
	t.Helper()
	now := time.Now().UTC()
	op := operations.Operation{
		ID: id.OperationID(), Status: operations.Planning, CreatedAt: now, UpdatedAt: now,
		Input: json.RawMessage(`{}`),
		Mission: missions.Mission{Name: "review", Objectives: []string{"Review findings"},
			InputSchema: json.RawMessage(`{}`), OutputSchema: json.RawMessage(`{}`),
			Policies: &policies.MissionPolicies{Continuation: &policies.ContinuationPolicy{
				AggregateResults: []string{"discover"}, ModelReview: []string{"discover", "inspect"},
			}}},
	}
	parent := operations.NewTask(op.ID, "", actions.NextAction{ActionName: "discover", Input: json.RawMessage(`{}`)})
	if _, err := op.QueueTask(parent); err != nil {
		t.Fatal(err)
	}
	if err := op.DispatchTask(operations.TaskDispatch{TaskID: parent.ID, AgentID: AgentID,
		StartedAt: now, DeadlineAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	output := Output("discover")
	if _, err := op.FinishTask(operations.TaskResult{TaskID: parent.ID, AgentID: AgentID,
		Status: operations.TaskSucceeded, Result: &output, FinishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	return op
}
