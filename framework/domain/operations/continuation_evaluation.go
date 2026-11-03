package operations

import (
	"errors"
	"fmt"
	"slices"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
)

type ContinuationEvaluation struct {
	Policy policies.ContinuationPolicy
	Task   Task
	// Tasks supplies group members by the parent's saved CreatedTaskIDs.
	// Other tasks, including descendants of those members, are ignored.
	Tasks []Task
}

type ContinuationDecision string

const (
	ContinuationWait      ContinuationDecision = "wait"
	ContinuationAutomatic ContinuationDecision = "automatic"
	ContinuationModel     ContinuationDecision = "model"
)

type ContinuationEffects struct {
	Decision        ContinuationDecision
	ProposedActions []actions.NextAction
	FollowUps       []ContinuationFollowUp
	// On a model decision, collect the children chosen by that decision.
	AggregateResults bool
}

type ContinuationFollowUp struct {
	TaskID           string
	Actions          []actions.NextAction
	AggregateResults bool
}

// EvaluateContinuation evaluates saved snapshots without mutation, I/O or generated IDs.
// The triggering atomic transition commits the effects before scheduling children.
func EvaluateContinuation(input ContinuationEvaluation) (ContinuationEffects, error) {
	if err := input.Policy.Validate(); err != nil {
		return ContinuationEffects{}, err
	}
	if c := input.Task.Continuation; c != nil && c.DecisionBy == DecisionCode && c.Status == ContinuationWaiting {
		return evaluateGroupContinuation(input)
	}
	return evaluateTaskContinuation(input.Policy, input.Task)
}

func evaluateTaskContinuation(policy policies.ContinuationPolicy, task Task) (ContinuationEffects, error) {
	switch task.Status {
	case TaskQueued, TaskRunning:
		return ContinuationEffects{Decision: ContinuationWait}, nil
	case TaskSucceeded, TaskFailed, TaskInterrupted, TaskCancelled:
	default:
		return ContinuationEffects{}, fmt.Errorf("cannot evaluate continuation for task status %q", task.Status)
	}
	effects := ContinuationEffects{Decision: ContinuationModel, AggregateResults: slices.Contains(policy.AggregateResults, task.ActionName)}
	if task.Status != TaskSucceeded || task.Result == nil {
		return effects, nil
	}
	proposed := task.Result.NextActions
	if len(proposed) == 0 || actions.ValidateNextActions(proposed) != nil || slices.Contains(policy.ModelReview, task.ActionName) {
		effects.ProposedActions = actions.CloneNextActions(proposed)
		return effects, nil
	}
	return ContinuationEffects{Decision: ContinuationAutomatic, FollowUps: []ContinuationFollowUp{{
		TaskID: task.ID, Actions: actions.CloneNextActions(proposed), AggregateResults: effects.AggregateResults,
	}}}, nil
}

func evaluateGroupContinuation(input ContinuationEvaluation) (ContinuationEffects, error) {
	children, err := continuationGroupMembers(input)
	if err != nil {
		return ContinuationEffects{}, err
	}
	if children == nil {
		return ContinuationEffects{Decision: ContinuationWait}, nil
	}
	effects := ContinuationEffects{Decision: ContinuationAutomatic}
	var proposals []actions.NextAction
	for _, child := range children {
		decision, err := evaluateTaskContinuation(input.Policy, child)
		if err != nil {
			return ContinuationEffects{}, err
		}
		if decision.Decision == ContinuationWait {
			return ContinuationEffects{Decision: ContinuationWait}, nil
		}
		if decision.Decision == ContinuationModel {
			effects.Decision = ContinuationModel
			proposals = append(proposals, decision.ProposedActions...)
		} else {
			effects.FollowUps = append(effects.FollowUps, decision.FollowUps...)
			for _, next := range decision.FollowUps {
				proposals = append(proposals, next.Actions...)
			}
		}
	}
	if effects.Decision == ContinuationModel {
		return ContinuationEffects{Decision: ContinuationModel, ProposedActions: proposals,
			AggregateResults: slices.Contains(input.Policy.AggregateResults, input.Task.ActionName)}, nil
	}
	return effects, nil
}

func continuationGroupMembers(input ContinuationEvaluation) ([]Task, error) {
	ids := input.Task.Continuation.CreatedTaskIDs
	if len(ids) == 0 {
		return nil, errors.New("waiting continuation group requires child task IDs")
	}
	members := make(map[string]Task, len(ids))
	for _, id := range ids {
		if id == "" || id == input.Task.ID {
			return nil, errors.New("continuation group requires distinct child task IDs")
		}
		if _, exists := members[id]; exists {
			return nil, fmt.Errorf("duplicate continuation group member %q", id)
		}
		members[id] = Task{}
	}
	for _, task := range input.Tasks {
		member, required := members[task.ID]
		if !required {
			continue
		}
		if member.ID != "" || task.ParentTaskID != input.Task.ID || task.OperationID != input.Task.OperationID {
			return nil, fmt.Errorf("invalid continuation group member %q", task.ID)
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
