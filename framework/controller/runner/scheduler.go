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
	records, err := r.catalog.Agents(ctx)
	if err != nil {
		return
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
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
			if task.Status != operations.TaskQueued || s.operation.BlockedByQuestion(task) {
				continue
			}
			agentID := r.selectAgent(task, records, used)
			if agentID == "" {
				continue
			}
			startedAt := time.Now().UTC()
			next, err := r.store.DispatchTask(ctx, s.operation.ID, operations.TaskDispatch{
				TaskID: task.ID, AgentID: agentID, StartedAt: startedAt, DeadlineAt: startedAt.Add(r.taskTimeout),
			})
			if err := s.acceptUpdate(next, err); err != nil {
				break // Retry unsaved dispatch on the next tick.
			}
			task = s.operation.Tasks[taskIndex(&s.operation, task.ID)]
			used[agentID]++
			r.lastAgent = agentID
			call := actionmodel.ActionCall{ActionName: task.ActionName, Input: append([]byte(nil), task.Input...), Work: messages.WorkContext{OperationID: task.OperationID, TaskID: task.ID, StepID: task.StepID}}
			r.workers++
			go r.runTask(task, call)
		}
	}
}

func (r *Runner) selectAgent(task operations.Task, records []agentmodel.Agent, used map[string]int) string {
	eligible := make([]string, 0, len(records))
	for _, record := range records {
		if record.Stale || record.Status != agentmodel.AgentReady || used[record.ID] >= r.capacity {
			continue
		}
		for _, action := range record.Actions {
			if action.Name != task.ActionName {
				continue
			}
			if operations.AssessAgentAction(record, action).Reason == "" {
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
