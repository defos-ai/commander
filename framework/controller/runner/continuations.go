package runner

import (
	"context"
	"errors"
	"slices"
	"time"

	commands "commander/framework/comms/clients/controller/commands"
	actionmodel "commander/framework/domain/actions"
	"commander/framework/domain/missions"
	"commander/framework/domain/operations"
	"commander/framework/id"
)

func queuedTask(operationID, stepID, parentID string, action actionmodel.NextAction) operations.Task {
	taskID := id.TaskID()
	return operations.Task{ID: taskID, CommandID: taskID, OperationID: operationID, StepID: stepID,
		ParentTaskID: parentID, AgentID: action.AgentID,
		ActionName: action.ActionName, Input: append([]byte(nil), action.Input...), Status: operations.TaskQueued}
}

// continueTask is part of the same operation update as the result. No publication
// happens here: child IDs and the decision are committed before the scheduler runs.
func (r *Runner) continueTask(op *operations.Operation, taskID string, success bool) {
	index := taskIndex(op, taskID)
	if index < 0 {
		return
	}
	task := &op.Tasks[index]
	previous := task.Continuation
	if previous != nil && (previous.Status != operations.ContinuationPending || previous.QuestionID != "") {
		return
	}
	c := &operations.Continuation{Version: 1, DecisionBy: operations.DecisionModel, Status: operations.ContinuationPending,
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
		c.DecisionBy, c.Status = operations.DecisionCode, operations.ContinuationResolved
		return
	}
	if op.Status.Terminal() {
		return
	}
	rule, automatic := automaticRule(op.Mission, *task)
	if !automatic || len(op.Tasks)+len(c.ProposedActions) > 10000 {
		return
	}
	c.DecisionBy = operations.DecisionCode
	createChildren(op, index, c.ProposedActions, rule.Continue == missions.WhenAll)
}

func createChildren(op *operations.Operation, parentIndex int, actions []actionmodel.NextAction, collect bool) {
	parent := op.Tasks[parentIndex]
	c := parent.Continuation
	for _, action := range actions {
		task := queuedTask(op.ID, parent.StepID, parent.ID, action)
		c.CreatedTaskIDs = append(c.CreatedTaskIDs, task.ID)
		op.Tasks = append(op.Tasks, task)
		appendEvent(op, operations.TaskAssigned, task.ID, "", "Follow-up task queued")
	}
	c.Status = operations.ContinuationResolved
	if collect && len(actions) > 0 {
		c.Status = operations.ContinuationWaiting
	}
}

// Collection belongs to the parent's durable continuation, not to each child.
func parentCollects(op *operations.Operation, task operations.Task) bool {
	if task.ParentTaskID == "" {
		return false
	}
	i := taskIndex(op, task.ParentTaskID)
	if i < 0 {
		return false
	}
	c := op.Tasks[i].Continuation
	return c != nil && c.DecisionBy == operations.DecisionCode &&
		c.Status == operations.ContinuationWaiting && slices.Contains(c.CreatedTaskIDs, task.ID)
}

func taskIndex(op *operations.Operation, taskID string) int {
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
func (r *Runner) refreshGroups(op *operations.Operation) {
	for i := 0; i < len(op.Tasks); i++ {
		c := op.Tasks[i].Continuation
		if c == nil || c.Status != operations.ContinuationWaiting || c.DecisionBy != operations.DecisionCode {
			continue
		}
		ready, automatic := true, !op.Status.Terminal()
		var proposals []actionmodel.NextAction
		var children []int
		for _, childID := range c.CreatedTaskIDs {
			j := taskIndex(op, childID)
			if j < 0 || op.Tasks[j].Status == operations.TaskQueued || op.Tasks[j].Status == operations.TaskRunning {
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
			c.Status = operations.ContinuationResolved
			for _, j := range children {
				rule, _ := automaticRule(op.Mission, op.Tasks[j])
				child := op.Tasks[j].Continuation
				child.Version++
				createChildren(op, j, child.ProposedActions, rule.Continue == missions.WhenAll)
			}
			appendEvent(op, operations.ContinuationResolvedEvent, op.Tasks[i].ID, "", "Task group settled; automatic rules applied")
		} else {
			c.DecisionBy, c.Status = operations.DecisionModel, operations.ContinuationPending
			c.ProposedActions = append([]actionmodel.NextAction{}, proposals...)
			appendEvent(op, operations.ContinuationReady, op.Tasks[i].ID, "", "Task group settled; model decision is required")
		}
	}
}

func (r *Runner) Resolve(ctx context.Context, command commands.ResolveContinuationCommand) (operations.Operation, error) {
	if err := command.Validate(); err != nil {
		return operations.Operation{}, err
	}
	command.Actions = actionmodel.CloneNextActions(command.Actions)
	result, err := r.change(ctx, command.OperationID, func(op *operations.Operation) error {
		i := taskIndex(op, command.TaskID)
		if i < 0 {
			return errors.New("unknown task")
		}
		c := op.Tasks[i].Continuation
		if c == nil || c.Version != command.Version || c.Status != operations.ContinuationPending || c.DecisionBy != operations.DecisionModel {
			return errors.New("continuation is not pending at the supplied version; refresh get_operation")
		}
		for _, q := range op.Questions {
			if q.Pending() && (q.TaskID == command.TaskID || q.TaskID == "" && (q.StepID == "" || q.StepID == op.Tasks[i].StepID)) {
				return ErrQuestionPending
			}
		}
		if len(op.Tasks)+len(command.Actions) > 10000 {
			return errors.New("operation task limit exceeded")
		}
		c.ProposedActions = actionmodel.CloneNextActions(command.Actions)
		c.Version++
		createChildren(op, i, command.Actions, false)
		refreshStatus(op)
		appendEvent(op, operations.ContinuationResolvedEvent, command.TaskID, "", "Model decision recorded")
		return nil
	})
	if err == nil {
		r.signalScheduler()
	}
	return result, err
}

func cancelQueued(op *operations.Operation) {
	now := time.Now().UTC()
	for i := range op.Tasks {
		if op.Tasks[i].Status == operations.TaskQueued {
			op.Tasks[i].Status = operations.TaskCancelled
			op.Tasks[i].FinishedAt = &now
		}
	}
}
