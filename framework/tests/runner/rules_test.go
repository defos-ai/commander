package runner_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func TestRuleApplierAutomaticallyPreservesEveryProposal(t *testing.T) {
	task := ruleTask("batch", "batch-ports",
		actions.NextAction{ActionName: "scan-ports", Input: json.RawMessage(`{"port":80}`)},
		actions.NextAction{ActionName: "scan-ports", Input: json.RawMessage(`{"port":443}`)},
		actions.NextAction{ActionName: "scan-ports", Input: json.RawMessage(`{"port":80}`)})
	before := task.Clone()
	input := operations.RuleContext{Task: task}
	effects := applyRules(t, input)
	if effects.Decision != operations.RuleAutomatic || len(effects.FollowUps) != 1 {
		t.Fatalf("expected one automatic batch: %+v", effects)
	}
	batch := effects.FollowUps[0]
	if batch.TaskID != task.ID || batch.AggregateResults || !reflect.DeepEqual(batch.Actions, task.Result.NextActions) {
		t.Fatalf("proposals lost their source, order or inputs: %+v", batch)
	}
	if repeated := applyRules(t, input); !reflect.DeepEqual(effects, repeated) {
		t.Fatalf("same snapshot produced different effects: %+v, %+v", effects, repeated)
	}
	effects.FollowUps[0].Actions[0].ActionName = "changed"
	effects.FollowUps[0].Actions[0].Input[0] = '['
	if !reflect.DeepEqual(task, before) {
		t.Fatal("applying rules or modifying their effects mutated the source task")
	}
	if next := applyRules(t, input); !reflect.DeepEqual(next.FollowUps[0].Actions, before.Result.NextActions) {
		t.Fatal("later evaluation reused effects modified by a caller")
	}
}

func TestRuleApplierDefersUnfinishedWorkAndRequestsReview(t *testing.T) {
	cases := []struct {
		name   string
		status operations.TaskStatus
		result *actions.ActionOutput
		want   operations.RuleDecision
	}{
		{"queued", operations.TaskQueued, nil, operations.RuleWait},
		{"running", operations.TaskRunning, nil, operations.RuleWait},
		{"missing result", operations.TaskSucceeded, nil, operations.RuleModel},
		{"failed", operations.TaskFailed, &actions.ActionOutput{NextActions: []actions.NextAction{{ActionName: "retry"}}}, operations.RuleModel},
		{"interrupted", operations.TaskInterrupted, nil, operations.RuleModel},
		{"cancelled", operations.TaskCancelled, nil, operations.RuleModel},
		{"empty proposals", operations.TaskSucceeded, &actions.ActionOutput{}, operations.RuleModel},
		{"invalid action", operations.TaskSucceeded, &actions.ActionOutput{NextActions: []actions.NextAction{{ActionName: "valid"}, {ActionName: " "}}}, operations.RuleModel},
		{"invalid input", operations.TaskSucceeded, &actions.ActionOutput{NextActions: []actions.NextAction{{ActionName: "scan", Input: json.RawMessage(`[]`)}}}, operations.RuleModel},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			effects := applyRules(t, operations.RuleContext{Task: operations.Task{ID: "task", Status: tc.status, Result: tc.result}})
			if effects.Decision != tc.want || len(effects.FollowUps) != 0 {
				t.Fatalf("unexpected dispatch or decision: %+v", effects)
			}
		})
	}
}

func TestRuleApplierMatchesSourceActionsAndPreservesAggregation(t *testing.T) {
	rules := missions.RuleSet{
		missions.AggregateResults:  {Actions: []string{"batch-ports", "scan-ports"}},
		missions.DecideNextActions: {Actions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	cases := []struct {
		action    string
		decision  operations.RuleDecision
		aggregate bool
	}{
		{"batch-ports", operations.RuleAutomatic, true},
		{"scan-ports", operations.RuleModel, true},
		{"scan-ports-other", operations.RuleAutomatic, false},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			task := ruleTask("source", tc.action, actions.NextAction{ActionName: "scan-ports"})
			effects := applyRules(t, operations.RuleContext{Rules: rules, Task: task})
			if effects.Decision != tc.decision {
				t.Fatalf("rule applied to the wrong source: %+v", effects)
			}
			aggregate := effects.AggregateResults
			if effects.Decision == operations.RuleAutomatic {
				if len(effects.FollowUps) != 1 {
					t.Fatalf("expected a single follow-up batch: %+v", effects)
				}
				aggregate = effects.FollowUps[0].AggregateResults
			} else if !reflect.DeepEqual(effects.ProposedActions, task.Result.NextActions) {
				t.Fatalf("model decision lost proposals: %+v", effects)
			}
			if aggregate != tc.aggregate {
				t.Fatalf("aggregation=%v, want %v", aggregate, tc.aggregate)
			}
		})
	}
}

func TestRuleApplierUsesDurableGroupMembersAndTheirOwnRules(t *testing.T) {
	first := ruleTask("first", "scan-ports", actions.NextAction{ActionName: "report-scan"})
	second := ruleTask("second", "batch-ports", actions.NextAction{ActionName: "scan-ports"})
	input := ruleGroup(first, second)
	input.Task.ActionName = "old-batch-action"
	input.Task.Continuation.CreatedTaskIDs = []string{second.ID, first.ID}
	input.Rules = missions.RuleSet{missions.AggregateResults: {Actions: []string{"batch-ports"}}}
	otherExecution := ruleTask("other-execution", "batch-ports")
	otherExecution.ParentTaskID = "another-parent"
	unrecordedChild := ruleTask("unrecorded", "scan-ports")
	unrecordedChild.ParentTaskID = input.Task.ID
	unrecordedChild.Status = operations.TaskFailed
	grandchild := ruleTask("grandchild", "scan-ports")
	grandchild.ParentTaskID = first.ID
	grandchild.Status = operations.TaskRunning
	input.Tasks = append(input.Tasks, otherExecution, unrecordedChild, grandchild)

	effects := applyRules(t, input)
	if effects.Decision != operations.RuleAutomatic || len(effects.FollowUps) != 2 {
		t.Fatalf("unrelated work interfered with the saved group: %+v", effects)
	}
	if effects.FollowUps[0].TaskID != second.ID || !effects.FollowUps[0].AggregateResults ||
		effects.FollowUps[1].TaskID != first.ID || effects.FollowUps[1].AggregateResults {
		t.Fatalf("children lost their source, saved ordering or aggregation rules: %+v", effects.FollowUps)
	}
	if !reflect.DeepEqual(effects.FollowUps[0].Actions, second.Result.NextActions) ||
		!reflect.DeepEqual(effects.FollowUps[1].Actions, first.Result.NextActions) {
		t.Fatalf("follow-ups no longer belong to their original child: %+v", effects.FollowUps)
	}
}

func TestRuleApplierWaitsForWholeGroupBeforeModelReview(t *testing.T) {
	for _, state := range []string{"missing", "running"} {
		t.Run(state, func(t *testing.T) {
			input := ruleGroup(ruleTask("review", "scan-ports"), ruleTask("pending", "batch-ports"))
			input.Rules = missions.RuleSet{missions.DecideNextActions: {Actions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}}}
			if state == "missing" {
				input.Tasks = input.Tasks[:1]
			} else {
				input.Tasks[1].Status = operations.TaskRunning
			}
			effects := applyRules(t, input)
			if effects.Decision != operations.RuleWait || len(effects.FollowUps) != 0 || len(effects.ProposedActions) != 0 {
				t.Fatalf("unfinished group exposed an actionable decision: %+v", effects)
			}
		})
	}
}

func TestRuleApplierReturnsOneModelDecisionForMixedGroup(t *testing.T) {
	first := ruleTask("first", "batch-ports", actions.NextAction{ActionName: "scan-ports", Input: json.RawMessage(`{"port":80}`)})
	second := ruleTask("second", "scan-ports", actions.NextAction{ActionName: "report-scan", Input: json.RawMessage(`{"port":443}`)})
	input := ruleGroup(first, second)
	input.Rules = missions.RuleSet{
		missions.AggregateResults:  {Actions: []string{"batch-ports"}},
		missions.DecideNextActions: {Actions: []string{"scan-ports"}, Params: &missions.RuleParams{DecisionBy: missions.Model}},
	}
	before := []operations.Task{input.Tasks[0].Clone(), input.Tasks[1].Clone()}
	effects := applyRules(t, input)
	want := append(actions.CloneNextActions(first.Result.NextActions), actions.CloneNextActions(second.Result.NextActions)...)
	if effects.Decision != operations.RuleModel || len(effects.FollowUps) != 0 || !effects.AggregateResults || !reflect.DeepEqual(effects.ProposedActions, want) {
		t.Fatalf("group was split between automatic execution and model review: %+v", effects)
	}
	if repeated := applyRules(t, input); !reflect.DeepEqual(effects, repeated) {
		t.Fatal("re-evaluating the same group changed the decision")
	}
	for i := range effects.ProposedActions {
		effects.ProposedActions[i].ActionName = "changed"
		effects.ProposedActions[i].Input[0] = '['
	}
	if !reflect.DeepEqual(input.Tasks, before) {
		t.Fatal("group evaluation or modifying model proposals mutated saved child results")
	}
}

func TestRuleApplierFailedGroupPreservesSuccessfulSiblingProposals(t *testing.T) {
	succeeded := ruleTask("succeeded", "scan-ports", actions.NextAction{ActionName: "report-scan"})
	failed := ruleTask("failed", "scan-ports", actions.NextAction{ActionName: "unaccepted-proposal"})
	failed.Status = operations.TaskFailed
	effects := applyRules(t, ruleGroup(succeeded, failed))
	if effects.Decision != operations.RuleModel || len(effects.FollowUps) != 0 || !reflect.DeepEqual(effects.ProposedActions, succeeded.Result.NextActions) {
		t.Fatalf("failed group dispatched work or lost successful sibling proposals: %+v", effects)
	}
}

func TestRuleApplierRejectsInvalidGroupsWithoutEffects(t *testing.T) {
	cases := []struct {
		name   string
		modify func(*operations.RuleContext)
	}{
		{"no members", func(input *operations.RuleContext) { input.Task.Continuation.CreatedTaskIDs = nil }},
		{"duplicate member ID", func(input *operations.RuleContext) {
			input.Task.Continuation.CreatedTaskIDs = []string{"child", "child"}
		}},
		{"self reference", func(input *operations.RuleContext) { input.Task.Continuation.CreatedTaskIDs = []string{input.Task.ID} }},
		{"blank member ID", func(input *operations.RuleContext) { input.Task.Continuation.CreatedTaskIDs = []string{""} }},
		{"different parent", func(input *operations.RuleContext) { input.Tasks[0].ParentTaskID = "other-parent" }},
		{"different operation", func(input *operations.RuleContext) { input.Tasks[0].OperationID = "other-operation" }},
		{"duplicate snapshot", func(input *operations.RuleContext) { input.Tasks = append(input.Tasks, input.Tasks[0].Clone()) }},
		{"unknown task status", func(input *operations.RuleContext) { input.Tasks[0].Status = "unknown" }},
		{"invalid rule", func(input *operations.RuleContext) {
			input.Rules = missions.RuleSet{"unknown-rule": {Actions: []string{"scan-ports"}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := ruleGroup(ruleTask("child", "scan-ports", actions.NextAction{ActionName: "report-scan"}))
			tc.modify(&input)
			effects, err := (operations.DefaultRuleApplier{}).Apply(input)
			if err == nil || !reflect.DeepEqual(effects, operations.RuleEffects{}) {
				t.Fatalf("invalid group produced effects=%+v, error=%v", effects, err)
			}
		})
	}
}

func applyRules(t *testing.T, input operations.RuleContext) operations.RuleEffects {
	t.Helper()
	effects, err := (operations.DefaultRuleApplier{}).Apply(input)
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func ruleTask(id, action string, next ...actions.NextAction) operations.Task {
	return operations.Task{ID: id, OperationID: "operation", ActionName: action, Status: operations.TaskSucceeded,
		Result: &actions.ActionOutput{ActionName: action, NextActions: next}}
}

func ruleGroup(children ...operations.Task) operations.RuleContext {
	parent := ruleTask("parent", "batch-ports")
	parent.Continuation = &operations.Continuation{DecisionBy: operations.DecisionCode, Status: operations.ContinuationWaiting}
	for i := range children {
		children[i].ParentTaskID = parent.ID
		parent.Continuation.CreatedTaskIDs = append(parent.Continuation.CreatedTaskIDs, children[i].ID)
	}
	return operations.RuleContext{Task: parent, Tasks: children}
}
