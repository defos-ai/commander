package sharing

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/defos-ai/commander/framework/domain/operations"
)

// Cleanup must not depend on a finish notification surviving the bounded queue.
func (w *Worker) pruneSavedBatches(ctx context.Context) error {
	w.pruneExpiredBatches()
	seen := make(map[string]bool)
	for _, group := range w.batches {
		if seen[group.operationID] {
			continue
		}
		seen[group.operationID] = true
		op, err := w.operationStore.Get(ctx, group.operationID)
		if errors.Is(err, operations.ErrUnknownOperation) {
			w.forgetOperation(group.operationID)
			continue
		}
		if err != nil {
			return err
		}
		w.pruneOperationBatches(op, 0)
	}
	return nil
}

func (w *Worker) pruneExpiredBatches() {
	for batchID, group := range w.batches {
		group.recipients = slices.DeleteFunc(group.recipients, func(recipient batchRecipient) bool {
			return recipient.task.DeadlineAt == nil || !time.Now().Before(*recipient.task.DeadlineAt)
		})
		w.retainBatch(batchID, group)
	}
}

func (w *Worker) pruneOperationBatches(op operations.Operation, through uint64) {
	if op.Status.Terminal() {
		w.forgetOperation(op.ID)
		return
	}
	for _, event := range op.Events {
		// A sweep cannot remove a batch whose queued finish is still waiting.
		if event.Type == operations.TaskBatchFinished && event.Sequence <= through {
			delete(w.batches, event.BatchID)
		}
	}
	for batchID, group := range w.batches {
		if group.operationID != op.ID {
			continue
		}
		group.recipients = slices.DeleteFunc(group.recipients, func(recipient batchRecipient) bool {
			return !slices.ContainsFunc(op.Tasks, func(task operations.Task) bool {
				return task.ID == recipient.task.ID && task.AgentID == recipient.task.AgentID &&
					task.CommandID == recipient.task.CommandID && task.Status == operations.TaskRunning &&
					task.DeadlineAt != nil && time.Now().Before(*task.DeadlineAt)
			})
		})
		w.retainBatch(batchID, group)
	}
}

func (w *Worker) retainBatch(batchID string, group batchRecipients) {
	if len(group.recipients) == 0 {
		delete(w.batches, batchID)
	} else {
		w.batches[batchID] = group
	}
}
