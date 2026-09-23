package runner

import (
	"context"
	"sort"
	"time"

	"commander/framework/comms/messages"
	actionmodel "commander/framework/domain/actions"
	agentmodel "commander/framework/domain/agents"
	"commander/framework/domain/operations"
)

func (r *Runner) signalScheduler() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) dispatchQueued() {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	var records []agentmodel.Agent
	if r.catalog != nil {
		var err error
		records, err = r.catalog.Agents(ctx)
		if err != nil {
			return
		} // Keep durable work queued until inventory is available.
		sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	}
	if r.ctx.Err() != nil {
		return
	}
	used := map[string]int{}
	ids := make([]string, 0, len(r.states))
	for id, s := range r.states {
		ids = append(ids, id)
		for _, task := range s.operation.Tasks {
			if task.Status == operations.TaskRunning {
				used[task.AgentID]++
			}
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := r.states[id]
		if active(s) != nil {
			continue
		}
		if err := r.refreshPreflight(ctx, s, records); err != nil {
			continue
		}
		if s.operation.Preflight != nil && !s.operation.Preflight.Ready {
			continue
		}
		for _, task := range s.operation.Tasks {
			if task.Status != operations.TaskQueued || blockedByQuestion(s.operation, task) {
				continue
			}
			agentID := r.selectAgent(task, records, used)
			if agentID == "" {
				continue
			}
			if err := r.update(ctx, s, func(next *operations.Operation) error {
				i := taskIndex(next, task.ID)
				if next.Status.Terminal() || i < 0 || next.Tasks[i].Status != operations.TaskQueued || blockedByQuestion(*next, next.Tasks[i]) || next.Preflight != nil && !next.Preflight.Ready {
					return operations.ErrOperationConflict
				}
				task = next.Tasks[i]
				task.AgentID, task.Status, task.StartedAt = agentID, operations.TaskRunning, time.Now().UTC()
				deadline := task.StartedAt.Add(r.taskTimeout)
				task.DeadlineAt = &deadline
				next.Tasks[i] = task
				refreshStatus(next)
				appendEvent(next, operations.TaskDispatched, task.ID, "", "Task dispatched")
				return nil
			}); err != nil {
				break // Retry unsaved dispatch on the next tick.
			}
			used[agentID]++
			r.lastAgent = agentID
			call := actionmodel.ActionCall{ActionName: task.ActionName, Input: append([]byte(nil), task.Input...), Work: messages.WorkContext{OperationID: task.OperationID, TaskID: task.ID, StepID: task.StepID}}
			r.workers++
			go r.runTask(task, call)
		}
	}
}

func blockedByQuestion(op operations.Operation, task operations.Task) bool {
	for _, q := range op.Questions {
		if !q.Pending() {
			continue
		}
		if q.TaskID != "" {
			if task.ParentTaskID == q.TaskID {
				return true
			}
			continue
		}
		if q.StepID == "" || q.StepID == task.StepID {
			return true
		}
	}
	return false
}

func (r *Runner) selectAgent(task operations.Task, records []agentmodel.Agent, used map[string]int) string {
	// An injected executor without a catalog can only receive explicitly targeted tasks.
	if r.catalog == nil {
		if task.AgentID != "" && used[task.AgentID] < r.capacity {
			return task.AgentID
		}
		return ""
	}
	eligible := make([]string, 0, len(records))
	for _, record := range records {
		if record.Stale || record.Status != agentmodel.AgentReady || used[record.ID] >= r.capacity || task.AgentID != "" && task.AgentID != record.ID {
			continue
		}
		for _, action := range record.Actions {
			if action.Name != task.ActionName {
				continue
			}
			if assessAgentAction(record, action).Reason == "" {
				eligible = append(eligible, record.ID)
			}
			break
		}
	}
	for _, id := range eligible {
		if id > r.lastAgent {
			return id
		}
	}
	if len(eligible) > 0 {
		return eligible[0]
	}
	return ""
}
