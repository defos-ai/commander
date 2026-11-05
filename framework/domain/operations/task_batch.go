package operations

import (
	"fmt"

	"github.com/defos-ai/commander/framework/id"
)

func (o Operation) batchMembers() map[string][]Task {
	members := make(map[string][]Task)
	for _, task := range o.Tasks {
		if task.BatchID != "" {
			members[task.BatchID] = append(members[task.BatchID], task)
		}
	}
	return members
}

func batchSettled(members []Task) bool {
	if len(members) == 0 {
		return false
	}
	for _, task := range members {
		switch task.Status {
		case TaskSucceeded, TaskFailed, TaskCancelled:
		default:
			return false
		}
	}
	return true
}

func (o *Operation) appendBatchEvent(kind EventType, batchID, parentID, message string) {
	o.AppendEvent(kind, parentID, "", message)
	o.Events[len(o.Events)-1].BatchID = batchID
}

func (o *Operation) finishBatches() {
	members := o.batchMembers()
	if len(members) == 0 {
		return
	}
	finished := make(map[string]bool)
	for _, event := range o.Events {
		if event.Type == TaskBatchFinished {
			finished[event.BatchID] = true
		}
	}
	// Task membership survives later continuation decisions and their new child lists.
	for _, event := range o.Events {
		if event.Type == TaskBatchStarted && !finished[event.BatchID] && batchSettled(members[event.BatchID]) {
			o.appendBatchEvent(TaskBatchFinished, event.BatchID, event.TaskID, "Aggregation batch finished; all producer outcomes are known")
			finished[event.BatchID] = true
		}
	}
}

func (o Operation) validateBatches() error {
	members := o.batchMembers()
	parents := make(map[string]bool, len(o.Tasks))
	for _, task := range o.Tasks {
		parents[task.ID] = true
	}
	started, finished := make(map[string]Event), make(map[string]bool)
	for _, event := range o.Events {
		if event.Type != TaskBatchStarted && event.Type != TaskBatchFinished {
			continue
		}
		if !id.Valid(event.BatchID) || event.OperationID != o.ID || event.CreatedAt.IsZero() || !parents[event.TaskID] {
			return fmt.Errorf("invalid batch event %d", event.Sequence)
		}
		previous, exists := started[event.BatchID]
		if event.Type == TaskBatchStarted {
			if exists || len(members[event.BatchID]) == 0 {
				return fmt.Errorf("duplicate or empty batch %s", event.BatchID)
			}
			started[event.BatchID] = event
		} else {
			if !exists || finished[event.BatchID] || event.TaskID != previous.TaskID || event.Sequence <= previous.Sequence || !batchSettled(members[event.BatchID]) {
				return fmt.Errorf("invalid batch completion %s", event.BatchID)
			}
			finished[event.BatchID] = true
		}
	}
	for batchID, tasks := range members {
		start, exists := started[batchID]
		if !exists {
			return fmt.Errorf("batch %s has no start event", batchID)
		}
		for _, task := range tasks {
			if task.ID == start.TaskID || task.ParentTaskID != start.TaskID || task.OperationID != o.ID {
				return fmt.Errorf("batch %s contains unrelated task %s", batchID, task.ID)
			}
		}
		if batchSettled(tasks) && !finished[batchID] {
			return fmt.Errorf("settled batch %s has no finish event", batchID)
		}
	}
	return nil
}
