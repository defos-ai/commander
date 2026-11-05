package sharing

import (
	"context"
	"errors"
	"slices"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/domain/missions/policies"
	"github.com/defos-ai/commander/framework/domain/operations"
)

type batchRecipients struct {
	operationID string
	recipients  []batchRecipient
}

type batchRecipient struct {
	task    operations.Task
	taskIDs []string
}

func (w *Worker) forwardOperationEvents(ctx context.Context, events []operations.Event) error {
	op, err := w.operationStore.Get(ctx, events[0].OperationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		w.forgetOperation(events[0].OperationID)
		return nil
	}
	if err != nil {
		return err
	}
	if op.ID != events[0].OperationID {
		return errors.New("batch sharing operation does not match its saved scope")
	}
	if op.Status.Terminal() {
		w.forgetOperation(op.ID)
		return nil
	}
	defer w.pruneOperationBatches(op, events[len(events)-1].Sequence)
	var forwardingErr error
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return errors.Join(forwardingErr, err)
		}
		if !savedSharingEvent(op, event) {
			return errors.Join(forwardingErr, errors.New("sharing event does not match its saved operation"))
		}
		switch event.Type {
		case operations.TaskBatchStarted:
			err = w.startBatch(ctx, op, event)
		case operations.TaskFinished:
			index := slices.IndexFunc(op.Tasks, func(task operations.Task) bool { return task.ID == event.TaskID })
			if index >= 0 {
				task := op.Tasks[index]
				if task.BatchID != "" && task.Status == operations.TaskSucceeded {
					err = w.forwardSharedResult(ctx, sharedData{OperationID: op.ID, TaskID: task.ID,
						AgentID: task.AgentID, CommandID: task.CommandID, BatchID: task.BatchID})
				}
			}
		case operations.TaskBatchFinished:
			err = w.finishBatch(ctx, op, event)
		case operations.QuestionAnsweredEvent:
			err = w.forwardSharedDecision(ctx, sharedData{OperationID: op.ID, QuestionID: event.QuestionID})
		}
		forwardingErr = errors.Join(forwardingErr, err)
		err = nil
	}
	return forwardingErr
}

func savedSharingEvent(op operations.Operation, event operations.Event) bool {
	if event.OperationID != op.ID || event.Sequence == 0 || event.Sequence > uint64(len(op.Events)) {
		return false
	}
	saved := op.Events[event.Sequence-1]
	return saved.Sequence == event.Sequence && saved.Type == event.Type && saved.TaskID == event.TaskID &&
		saved.BatchID == event.BatchID && saved.QuestionID == event.QuestionID && saved.CreatedAt.Equal(event.CreatedAt)
}

func (w *Worker) startBatch(ctx context.Context, op operations.Operation, event operations.Event) error {
	if _, exists := w.batches[event.BatchID]; exists {
		return nil
	}
	group := batchRecipients{operationID: op.ID}
	w.batches[event.BatchID] = group
	if op.Mission.Policies == nil || len(op.Mission.Policies.Sharing) == 0 {
		return nil
	}
	if err := op.Mission.Policies.Validate(); err != nil {
		return err
	}
	var members []operations.Task
	for _, task := range op.Tasks {
		if task.BatchID == event.BatchID && task.ParentTaskID == event.TaskID && task.OperationID == op.ID {
			members = append(members, task)
		}
	}
	var forwardingErr error
	for _, task := range op.Tasks {
		if task.ID == event.TaskID || task.StartedAt.IsZero() || task.StartedAt.After(event.CreatedAt) ||
			slices.ContainsFunc(members, func(member operations.Task) bool { return member.ID == task.ID }) {
			continue
		}
		var taskIDs []string
		for _, member := range members {
			if sharesAction(op.Mission.Policies.Sharing, member.ActionName, task.ActionName) {
				taskIDs = append(taskIDs, member.ID)
			}
		}
		if len(taskIDs) == 0 {
			continue
		}
		active, err := w.sharedRecipient(ctx, task)
		if err != nil {
			forwardingErr = errors.Join(forwardingErr, err)
			continue
		}
		if !active {
			continue
		}
		update := w.batchUpdate(op.ID, event, taskIDs)
		if err := w.publishSharedUpdate(ctx, task, update); err != nil {
			forwardingErr = errors.Join(forwardingErr, err)
			continue
		}
		group.recipients = append(group.recipients, batchRecipient{task: task.Clone(), taskIDs: taskIDs})
	}
	w.batches[event.BatchID] = group
	return forwardingErr
}

func (w *Worker) finishBatch(ctx context.Context, op operations.Operation, event operations.Event) error {
	group, exists := w.batches[event.BatchID]
	if !exists || group.operationID != op.ID {
		return nil
	}
	defer delete(w.batches, event.BatchID)
	var forwardingErr error
	for _, recipient := range group.recipients {
		update := w.batchUpdate(op.ID, event, recipient.taskIDs)
		update.Batch.Outcomes = make(map[string]operations.TaskStatus, len(recipient.taskIDs))
		for _, task := range op.Tasks {
			if task.BatchID == event.BatchID && slices.Contains(recipient.taskIDs, task.ID) {
				update.Batch.Outcomes[task.ID] = task.Status
			}
		}
		forwardingErr = errors.Join(forwardingErr, w.publishSharedUpdate(ctx, recipient.task, update))
	}
	return forwardingErr
}

func (w *Worker) batchUpdate(operationID string, event operations.Event, taskIDs []string) agentevents.SharedData {
	return agentevents.SharedData{BatchID: event.BatchID,
		Source: agentevents.SharedDataSource{ControllerID: w.controllerID, OperationID: operationID, TaskID: event.TaskID},
		Batch:  &agentevents.TaskBatchUpdate{Type: event.Type, TaskIDs: slices.Clone(taskIDs)}}
}

func (w *Worker) batchRecipient(batchID, producerID string, task operations.Task) bool {
	group := w.batches[batchID]
	return group.operationID == task.OperationID && slices.ContainsFunc(group.recipients, func(recipient batchRecipient) bool {
		return recipient.task.ID == task.ID && recipient.task.AgentID == task.AgentID && recipient.task.CommandID == task.CommandID &&
			slices.Contains(recipient.taskIDs, producerID)
	})
}

func (w *Worker) forgetOperation(operationID string) {
	for batchID, group := range w.batches {
		if group.operationID == operationID {
			delete(w.batches, batchID)
		}
	}
}

func sharesAction(policy policies.SharingPolicy, from, to string) bool {
	return slices.ContainsFunc(policy, func(route policies.SharingRoute) bool {
		return slices.Contains(route.FromActions, from) && slices.Contains(route.ToActions, to)
	})
}
