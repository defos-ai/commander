package fixtures

import (
	"slices"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
)

func RuleTask(id, action string, next ...actions.NextAction) operations.Task {
	return operations.Task{ID: id, OperationID: "operation", ActionName: action, Status: operations.TaskSucceeded,
		Result: &actions.ActionOutput{ActionName: action, NextActions: actions.CloneNextActions(next)}}
}

func RuleGroup(children ...operations.Task) operations.RuleContext {
	children = slices.Clone(children)
	parent := RuleTask("parent", "batch-ports")
	parent.Continuation = &operations.Continuation{DecisionBy: operations.DecisionCode, Status: operations.ContinuationWaiting}
	for i := range children {
		children[i] = children[i].Clone()
		children[i].ParentTaskID = parent.ID
		parent.Continuation.CreatedTaskIDs = append(parent.Continuation.CreatedTaskIDs, children[i].ID)
	}
	return operations.RuleContext{Task: parent, Tasks: children}
}

func ModelRules(action string) missions.RuleSet {
	return missions.RuleSet{
		missions.DecideNextActions: {ForActions: []string{action}, Params: &missions.RuleParams{DecisionBy: policies.Model}},
	}
}

func SharingPolicy() policies.SharingPolicy {
	return policies.SharingPolicy{{
		FromActions: []string{"discover"},
		ToActions:   []string{"inspect"},
		Include:     []policies.SharingKind{policies.ShareResults, policies.ShareArtifacts},
	}}
}
