package operations

import (
	"errors"
	"slices"
	"time"

	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/missions"
)

// continueTask is part of the same operation update as the result. No publication
// happens here: child IDs and the decision are committed before the scheduler runs.
func continueTask(op *Operation, taskID string, success bool) error {
	index := taskIndex(op, taskID)
	if index < 0 {
		return nil
	}
	task := &op.Tasks[index]
	previous := task.Continuation
	if previous != nil && (previous.Status != ContinuationPending || previous.QuestionID != "") {
		return nil
	}
	c := &Continuation{Version: 1, DecisionBy: DecisionModel, Status: ContinuationPending,
		ProposedActions: []actionmodel.NextAction{}, CreatedTaskIDs: []string{}}
	if previous != nil {
		c.Version = previous.Version + 1
	}
	if success && task.Result != nil {
		c.ProposedActions = actionmodel.CloneNextActions(task.Result.NextActions)
		if c.ProposedActions == nil {
			c.ProposedActions = []actionmodel.NextAction{}
		}
	}
	task.Continuation = c
	if parentCollects(op, *task) {
		c.DecisionBy, c.Status = DecisionCode, ContinuationResolved
		return nil
	}
	if op.Status.Terminal() {
		return nil
	}
	effects, err := (DefaultRuleApplier{}).Apply(RuleContext{Rules: op.Mission.Rules, Task: *task})
	if err != nil {
		return err
	}
	if effects.Decision != RuleAutomatic || len(op.Tasks)+len(c.ProposedActions) > 10000 {
		return nil
	}
	c.DecisionBy = DecisionCode
	applyFollowUps(op, effects.FollowUps)
	return nil
}

func createChildren(op *Operation, parentIndex int, actions []actionmodel.NextAction, collect bool) {
	parent := op.Tasks[parentIndex]
	c := parent.Continuation
	// Each decision owns its group; earlier children remain in the task graph.
	c.CreatedTaskIDs = make([]string, 0, len(actions))
	for _, action := range actions {
		task := NewTask(op.ID, parent.ID, action)
		c.CreatedTaskIDs = append(c.CreatedTaskIDs, task.ID)
		op.Tasks = append(op.Tasks, task)
		op.AppendEvent(TaskAssigned, task.ID, "", "Follow-up task queued")
	}
	c.Status = ContinuationResolved
	if collect && len(actions) > 0 {
		c.DecisionBy, c.Status = DecisionCode, ContinuationWaiting
	}
}

// Collection belongs to the parent's durable continuation, not to each child.
func parentCollects(op *Operation, task Task) bool {
	if task.ParentTaskID == "" {
		return false
	}
	i := taskIndex(op, task.ParentTaskID)
	if i < 0 {
		return false
	}
	c := op.Tasks[i].Continuation
	return c != nil && c.DecisionBy == DecisionCode &&
		c.Status == ContinuationWaiting && slices.Contains(c.CreatedTaskIDs, task.ID)
}

func taskIndex(op *Operation, taskID string) int {
	for i := range op.Tasks {
		if op.Tasks[i].ID == taskID {
			return i
		}
	}
	return -1
}

// Collected siblings reach a decision together. If all can continue in code,
// apply each child's rule in this same durable update. Otherwise expose one
// model decision on the parent, retaining all child results and proposals.
func refreshGroups(op *Operation) error {
	for i := 0; i < len(op.Tasks); i++ {
		c := op.Tasks[i].Continuation
		if c == nil || c.Status != ContinuationWaiting || c.DecisionBy != DecisionCode {
			continue
		}
		effects, err := (DefaultRuleApplier{}).Apply(RuleContext{Rules: op.Mission.Rules, Task: op.Tasks[i], Tasks: op.Tasks})
		if err != nil {
			return err
		}
		if effects.Decision == RuleWait {
			continue
		}
		proposals := effects.ProposedActions
		for _, next := range effects.FollowUps {
			proposals = append(proposals, actionmodel.CloneNextActions(next.Actions)...)
		}
		c.Version++
		if effects.Decision == RuleAutomatic && !op.Status.Terminal() && len(op.Tasks)+len(proposals) <= 10000 {
			c.Status = ContinuationResolved
			applyFollowUps(op, effects.FollowUps)
			op.AppendEvent(ContinuationResolvedEvent, op.Tasks[i].ID, "", "Task group settled; automatic rules applied")
		} else {
			c.DecisionBy, c.Status = DecisionModel, ContinuationPending
			c.ProposedActions = append([]actionmodel.NextAction{}, proposals...)
			op.AppendEvent(ContinuationReady, op.Tasks[i].ID, "", "Task group settled; model decision is required")
		}
	}
	return nil
}

func applyFollowUps(op *Operation, followUps []RuleFollowUp) {
	for _, next := range followUps {
		i := taskIndex(op, next.TaskID)
		c := op.Tasks[i].Continuation
		if c.Status == ContinuationResolved {
			c.Version++
		}
		c.DecisionBy = DecisionCode
		createChildren(op, i, next.Actions, next.AggregateResults)
	}
}

func (op *Operation) ResolveContinuation(taskID string, version uint64, actions []actionmodel.NextAction) error {
	if op.Status.Terminal() {
		return ErrFinished
	}
	if version == 0 || actions == nil {
		return errors.New("continuation resolution requires a version and explicit actions")
	}
	if err := actionmodel.ValidateNextActions(actions); err != nil {
		return err
	}
	i := taskIndex(op, taskID)
	if i < 0 {
		return ErrUnknownTask
	}
	c := op.Tasks[i].Continuation
	if c == nil || c.Version != version || c.Status != ContinuationPending || c.DecisionBy != DecisionModel {
		return ErrContinuationConflict
	}
	for _, q := range op.Questions {
		if q.Pending() && (q.TaskID == taskID || q.TaskID == "") {
			return ErrQuestionPending
		}
	}
	if len(op.Tasks)+len(actions) > 10000 {
		return ErrTaskLimitExceeded
	}
	c.ProposedActions = actionmodel.CloneNextActions(actions)
	c.Version++
	createChildren(op, i, actions, ruleMatches(op.Mission.Rules, missions.AggregateResults, op.Tasks[i].ActionName))
	op.RefreshStatus()
	op.AppendEvent(ContinuationResolvedEvent, taskID, "", "Model decision recorded")
	op.UpdatedAt = time.Now().UTC()
	return nil
}
