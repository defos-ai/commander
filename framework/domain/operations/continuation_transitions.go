package operations

import (
	"errors"
	"slices"
	"time"

	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/id"
)

// NewTask constructs a queued assignment with a stable command identity.
func NewTask(operationID, stepID, parentID string, action actionmodel.NextAction) Task {
	taskID := id.TaskID()
	return Task{ID: taskID, CommandID: taskID, OperationID: operationID, StepID: stepID,
		ParentTaskID: parentID, AgentID: action.AgentID,
		ActionName: action.ActionName, Input: append([]byte(nil), action.Input...), Status: TaskQueued}
}

// continueTask is part of the same operation update as the result. No publication
// happens here: child IDs and the decision are committed before the scheduler runs.
func continueTask(op *Operation, taskID string, success bool) {
	index := taskIndex(op, taskID)
	if index < 0 {
		return
	}
	task := &op.Tasks[index]
	previous := task.Continuation
	if previous != nil && (previous.Status != ContinuationPending || previous.QuestionID != "") {
		return
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
		return
	}
	if op.Status.Terminal() {
		return
	}
	rule, automatic := automaticRule(op.Mission, *task)
	if !automatic || len(op.Tasks)+len(c.ProposedActions) > 10000 {
		return
	}
	c.DecisionBy = DecisionCode
	createChildren(op, index, c.ProposedActions, rule.Continue == missions.WhenAll)
}

func createChildren(op *Operation, parentIndex int, actions []actionmodel.NextAction, collect bool) {
	parent := op.Tasks[parentIndex]
	c := parent.Continuation
	for _, action := range actions {
		task := NewTask(op.ID, parent.StepID, parent.ID, action)
		c.CreatedTaskIDs = append(c.CreatedTaskIDs, task.ID)
		op.Tasks = append(op.Tasks, task)
		op.AppendEvent(TaskAssigned, task.ID, "", "Follow-up task queued")
	}
	c.Status = ContinuationResolved
	if collect && len(actions) > 0 {
		c.Status = ContinuationWaiting
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
func refreshGroups(op *Operation) {
	for i := 0; i < len(op.Tasks); i++ {
		c := op.Tasks[i].Continuation
		if c == nil || c.Status != ContinuationWaiting || c.DecisionBy != DecisionCode {
			continue
		}
		ready, automatic := true, !op.Status.Terminal()
		var proposals []actionmodel.NextAction
		var children []int
		for _, childID := range c.CreatedTaskIDs {
			j := taskIndex(op, childID)
			if j < 0 || op.Tasks[j].Status == TaskQueued || op.Tasks[j].Status == TaskRunning {
				ready = false
				break
			}
			children = append(children, j)
			child := op.Tasks[j]
			_, allowed := automaticRule(op.Mission, child)
			automatic = automatic && allowed
			if child.Continuation != nil {
				proposals = append(proposals, actionmodel.CloneNextActions(child.Continuation.ProposedActions)...)
			}
		}
		if !ready {
			continue
		}
		c.Version++
		if automatic && len(op.Tasks)+len(proposals) <= 10000 {
			c.Status = ContinuationResolved
			for _, j := range children {
				rule, _ := automaticRule(op.Mission, op.Tasks[j])
				child := op.Tasks[j].Continuation
				child.Version++
				createChildren(op, j, child.ProposedActions, rule.Continue == missions.WhenAll)
			}
			op.AppendEvent(ContinuationResolvedEvent, op.Tasks[i].ID, "", "Task group settled; automatic rules applied")
		} else {
			c.DecisionBy, c.Status = DecisionModel, ContinuationPending
			c.ProposedActions = append([]actionmodel.NextAction{}, proposals...)
			op.AppendEvent(ContinuationReady, op.Tasks[i].ID, "", "Task group settled; model decision is required")
		}
	}
}

// ResolveContinuation records a model decision and its queued follow-up tasks.
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
		return errors.New("unknown task")
	}
	c := op.Tasks[i].Continuation
	if c == nil || c.Version != version || c.Status != ContinuationPending || c.DecisionBy != DecisionModel {
		return errors.New("continuation is not pending at the supplied version; refresh get_operation")
	}
	for _, q := range op.Questions {
		if q.Pending() && (q.TaskID == taskID || q.TaskID == "" && (q.StepID == "" || q.StepID == op.Tasks[i].StepID)) {
			return ErrQuestionPending
		}
	}
	if len(op.Tasks)+len(actions) > 10000 {
		return errors.New("operation task limit exceeded")
	}
	c.ProposedActions = actionmodel.CloneNextActions(actions)
	c.Version++
	createChildren(op, i, actions, false)
	op.RefreshStatus()
	op.AppendEvent(ContinuationResolvedEvent, taskID, "", "Model decision recorded")
	op.UpdatedAt = time.Now().UTC()
	return nil
}
