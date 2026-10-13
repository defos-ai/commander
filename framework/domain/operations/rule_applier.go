package operations

import (
	"errors"
	"fmt"
	"slices"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
)

// RuleApplier evaluates snapshots without mutation, I/O or generated IDs. The
// runtime selects ungrouped tasks or waiting group parents, checks operation
// lifecycle constraints, and commits effects with the triggering transition.
type RuleApplier interface {
	Apply(RuleContext) (RuleEffects, error)
}

type RuleContext struct {
	Rules missions.RuleSet
	Task  Task
	// Tasks supplies group members by the parent's saved CreatedTaskIDs.
	// Other tasks, including descendants of those members, are ignored.
	Tasks []Task
}

type RuleDecision string

const (
	RuleWait      RuleDecision = "wait"
	RuleAutomatic RuleDecision = "automatic"
	RuleModel     RuleDecision = "model"
)

type RuleEffects struct {
	Decision        RuleDecision
	ProposedActions []actions.NextAction
	FollowUps       []RuleFollowUp
	// On a model decision, collect the children chosen by that decision.
	AggregateResults bool
}

type RuleFollowUp struct {
	TaskID           string
	Actions          []actions.NextAction
	AggregateResults bool
}

type DefaultRuleApplier struct{}

func (DefaultRuleApplier) Apply(input RuleContext) (RuleEffects, error) {
	if err := input.Rules.Validate(); err != nil {
		return RuleEffects{}, err
	}
	if c := input.Task.Continuation; c != nil && c.DecisionBy == DecisionCode && c.Status == ContinuationWaiting {
		return applyGroupRules(input)
	}
	return applyTaskRules(input.Rules, input.Task)
}

func applyTaskRules(rules missions.RuleSet, task Task) (RuleEffects, error) {
	switch task.Status {
	case TaskQueued, TaskRunning:
		return RuleEffects{Decision: RuleWait}, nil
	case TaskSucceeded, TaskFailed, TaskInterrupted, TaskCancelled:
	default:
		return RuleEffects{}, fmt.Errorf("cannot apply rules to task status %q", task.Status)
	}
	effects := RuleEffects{Decision: RuleModel, AggregateResults: ruleMatches(rules, missions.AggregateResults, task.ActionName)}
	if task.Status != TaskSucceeded || task.Result == nil {
		return effects, nil
	}
	proposed := task.Result.NextActions
	if len(proposed) == 0 || actions.ValidateNextActions(proposed) != nil || ruleMatches(rules, missions.DecideNextActions, task.ActionName) {
		effects.ProposedActions = actions.CloneNextActions(proposed)
		return effects, nil
	}
	return RuleEffects{Decision: RuleAutomatic, FollowUps: []RuleFollowUp{{
		TaskID: task.ID, Actions: actions.CloneNextActions(proposed), AggregateResults: effects.AggregateResults,
	}}}, nil
}

func applyGroupRules(input RuleContext) (RuleEffects, error) {
	children, err := ruleGroupMembers(input)
	if err != nil {
		return RuleEffects{}, err
	}
	if children == nil {
		return RuleEffects{Decision: RuleWait}, nil
	}
	effects := RuleEffects{Decision: RuleAutomatic}
	var proposals []actions.NextAction
	for _, child := range children {
		decision, err := applyTaskRules(input.Rules, child)
		if err != nil {
			return RuleEffects{}, err
		}
		if decision.Decision == RuleWait {
			return RuleEffects{Decision: RuleWait}, nil
		}
		if decision.Decision == RuleModel {
			effects.Decision = RuleModel
			proposals = append(proposals, decision.ProposedActions...)
		} else {
			effects.FollowUps = append(effects.FollowUps, decision.FollowUps...)
			for _, next := range decision.FollowUps {
				proposals = append(proposals, next.Actions...)
			}
		}
	}
	if effects.Decision == RuleModel {
		return RuleEffects{Decision: RuleModel, ProposedActions: proposals,
			AggregateResults: ruleMatches(input.Rules, missions.AggregateResults, input.Task.ActionName)}, nil
	}
	return effects, nil
}

func ruleGroupMembers(input RuleContext) ([]Task, error) {
	ids := input.Task.Continuation.CreatedTaskIDs
	if len(ids) == 0 {
		return nil, errors.New("waiting rule group requires child task IDs")
	}
	members := make(map[string]Task, len(ids))
	for _, id := range ids {
		if id == "" || id == input.Task.ID {
			return nil, errors.New("rule group requires distinct child task IDs")
		}
		if _, exists := members[id]; exists {
			return nil, fmt.Errorf("duplicate rule group member %q", id)
		}
		members[id] = Task{}
	}
	for _, task := range input.Tasks {
		member, required := members[task.ID]
		if !required {
			continue
		}
		if member.ID != "" || task.ParentTaskID != input.Task.ID || task.OperationID != input.Task.OperationID {
			return nil, fmt.Errorf("invalid rule group member %q", task.ID)
		}
		members[task.ID] = task
	}
	children := make([]Task, 0, len(ids))
	for _, id := range ids {
		if members[id].ID == "" {
			return nil, nil
		}
		children = append(children, members[id])
	}
	return children, nil
}

func ruleMatches(rules missions.RuleSet, name missions.RuleName, action string) bool {
	return slices.Contains(rules[name].ForActions, action)
}
