package operation_test

import (
	"encoding/json"
	"testing"

	"github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/operation"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/operation"
)

func TestSameActionContinuationsRetainRoutingAcrossRecoveryWithoutPinningAnAgent(t *testing.T) {
	for _, mode := range []string{"automatic proposals", "model decision"} {
		t.Run(mode, func(t *testing.T) {
			config := fixtures.Config()
			if mode == "model decision" {
				config.MissionStore.(*fakes.MissionStore).Change(func(m *missions.Mission) {
					m.Policies = &policies.MissionPolicies{Continuation: &policies.ContinuationPolicy{ModelReview: []string{"inspect"}}}
				})
			}
			native := fixtures.ReadyAgentOnHost(fixtures.AgentID, "native-host")
			target := fixtures.ReadyAgentOnHost("review-agent-00000002", "k8s-prod")
			replacement := fixtures.ReadyAgentOnHost("review-agent-00000003", target.Server.Hostname)
			catalog := config.AgentCatalog.(*fakes.AgentCatalog)
			catalog.Set(native, target)
			exec, calls := fakes.ControlledExecutor()
			first := fixtures.NewWorker(t, config, exec)
			op := fixtures.Start(t, first)
			selector := operations.TaskSelector{Hostname: target.Server.Hostname}
			parent, err := first.Assign(t.Context(), commands.AssignTaskCommand{
				OperationID: op.ID, ActionName: "inspect", Input: json.RawMessage(`{}`),
				Selector: &selector,
			})
			if err != nil {
				t.Fatal(err)
			}
			parentCall := fixtures.Receive(t, calls)
			if parentCall.Agent != target.ID {
				t.Fatalf("parent did not run on its selected host: %+v", parentCall)
			}
			proposals := []actions.NextAction{
				{ActionName: "inspect", Input: json.RawMessage(`{"page":1}`)},
				{ActionName: "inspect", Input: json.RawMessage(`{"page":2}`)},
				{ActionName: "follow-up", Input: json.RawMessage(`{}`)},
			}

			catalog.Set(native)
			parentCall.Reply <- fixtures.Output("inspect", proposals...)
			if mode == "model decision" {
				pending := fixtures.AwaitOperation(t, first, op.ID, func(op operations.Operation) bool {
					return op.Tasks[0].Status == operations.TaskSucceeded
				})
				decision := pending.Tasks[0].Continuation
				if len(pending.Tasks) != 1 || decision == nil || decision.Status != operations.ContinuationPending {
					t.Fatalf("proposed children bypassed model review: %+v", pending)
				}
				if _, err := first.Resolve(t.Context(), commands.ResolveContinuationCommand{
					OperationID: op.ID, TaskID: parent.ID, Version: decision.Version, Actions: proposals,
				}); err != nil {
					t.Fatal(err)
				}
			}
			different := fixtures.Receive(t, calls)
			if different.Agent != native.ID || different.Call.ActionName != "follow-up" {
				t.Fatalf("continuation lost its host filter or constrained a different action: %+v", different)
			}
			different.Reply <- fixtures.Output("follow-up")
			queued := fixtures.AwaitOperation(t, first, op.ID, func(op operations.Operation) bool {
				return len(op.Tasks) == 4 && op.Tasks[3].Status == operations.TaskSucceeded
			})

			for _, child := range queued.Tasks[1:3] {
				if child.ParentTaskID != parent.ID || child.Status != operations.TaskQueued || child.AgentID != "" || child.Selector == nil || *child.Selector != selector {
					t.Fatalf("same-action fan-out lost accepted routing or inherited the old owner: %+v", child)
				}
			}
			if queued.Tasks[3].Selector != nil || queued.Tasks[3].ParentTaskID != parent.ID {
				t.Fatalf("different-action continuation inherited unrelated routing: %+v", queued.Tasks[3])
			}

			fixtures.CloseWorker(t, first)
			catalog.Set(native, replacement)
			restarted := fixtures.NewWorker(t, config, exec)
			inputs := make(map[string]bool)
			for range 2 {
				child := fixtures.Receive(t, calls)
				if child.Agent != replacement.ID || child.Call.ActionName != "inspect" {
					t.Fatalf("recovered child reused the parent owner or escaped its host: %+v", child)
				}
				inputs[string(child.Call.Input)] = true
				child.Reply <- fixtures.Output("inspect")
			}
			settled := fixtures.AwaitOperation(t, restarted, op.ID, func(op operations.Operation) bool {
				return len(op.Tasks) == 4 && op.Tasks[1].Status == operations.TaskSucceeded && op.Tasks[2].Status == operations.TaskSucceeded
			})

			if len(inputs) != 2 || !inputs[string(proposals[0].Input)] || !inputs[string(proposals[1].Input)] {
				t.Fatalf("fan-out recovery duplicated or changed child input: %+v", inputs)
			}
			for _, child := range settled.Tasks[1:3] {
				if child.AgentID != replacement.ID || child.Selector == nil || child.Selector.Hostname != target.Server.Hostname {
					t.Fatalf("recovery did not retain each child's routing: %+v", child)
				}
			}
		})
	}
}

func TestContinuationFanOutSelectorsAreIndependentOfParentAndSiblings(t *testing.T) {
	op := fixtures.AggregationReview(t)
	op.Tasks[0].Selector = &operations.TaskSelector{Hostname: "k8s-prod"}
	parent := op.Tasks[0]

	err := op.ResolveContinuation(parent.ID, parent.Continuation.Version, []actions.NextAction{
		{ActionName: parent.ActionName, Input: json.RawMessage(`{"page":1}`)},
		{ActionName: parent.ActionName, Input: json.RawMessage(`{"page":2}`)},
		{ActionName: "inspect", Input: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(op.Tasks) != 4 || op.Tasks[1].Selector == nil || op.Tasks[2].Selector == nil || op.Tasks[3].Selector != nil {
		t.Fatalf("mixed continuation did not inherit routing by action name: %+v", op.Tasks)
	}
	for _, child := range op.Tasks[1:] {
		if child.Status != operations.TaskQueued || child.AgentID != "" || child.ParentTaskID != parent.ID {
			t.Fatalf("fan-out reused execution ownership: %+v", child)
		}
	}
	op.Tasks[1].Selector.Hostname = "changed-child"
	if op.Tasks[0].Selector.Hostname != "k8s-prod" || op.Tasks[2].Selector.Hostname != "k8s-prod" {
		t.Fatalf("child routing mutation changed parent or sibling: %+v", op.Tasks)
	}
	op.Tasks[0].Selector.Hostname = "changed-parent"
	if op.Tasks[1].Selector.Hostname != "changed-child" || op.Tasks[2].Selector.Hostname != "k8s-prod" {
		t.Fatalf("parent routing mutation changed accepted children: %+v", op.Tasks)
	}
}
