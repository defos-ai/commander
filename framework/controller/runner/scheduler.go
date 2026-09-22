package runner

import (
	"context"
	"slices"
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

func (r *Runner) schedule() {
	defer r.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.wake:
		case <-ticker.C:
		}
		r.dispatchQueued()
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return
	}
	used := map[string]int{}
	ids := make([]string, 0, len(r.states))
	for id, s := range r.states {
		ids = append(ids, id)
		s.mu.Lock()
		for _, task := range s.operation.Tasks {
			if task.Status == operations.TaskRunning {
				used[task.AgentID]++
			}
		}
		s.mu.Unlock()
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := r.states[id]
		s.mu.Lock()
		if active(s) != nil {
			s.mu.Unlock()
			continue
		}
		for i, task := range s.operation.Tasks {
			if task.Status != operations.TaskQueued || blockedByQuestion(s.operation, task) {
				continue
			}
			agentID := r.selectAgent(task, records, used)
			if agentID == "" {
				continue
			}
			next := s.operation.Clone()
			task = next.Tasks[i]
			task.AgentID, task.Status, task.StartedAt = agentID, operations.TaskRunning, time.Now().UTC()
			deadline := task.StartedAt.Add(r.taskTimeout)
			task.DeadlineAt = &deadline
			next.Tasks[i] = task
			refreshStatus(&next)
			appendEvent(&next, operations.TaskDispatched, task.ID, "", "Task dispatched")
			if err := r.save(ctx, s, next); err != nil {
				break
			} // Retry unsaved dispatch on the next tick.
			used[agentID]++
			r.lastAgent = agentID
			call := actionmodel.ActionCall{ActionName: task.ActionName, Input: task.Input, Work: messages.WorkContext{OperationID: task.OperationID, TaskID: task.ID, StepID: task.StepID}}
			r.wg.Add(1)
			go r.runTask(s, task, call)
		}
		s.mu.Unlock()
	}
}

func blockedByQuestion(op operations.Operation, task operations.Task) bool {
	for _, q := range op.Questions {
		if q.AnsweredAt != nil {
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
			if action.Name != task.ActionName || action.Status != actionmodel.Ready {
				continue
			}
			allowed := true
			for _, permission := range action.RequiredPermissions {
				if !slices.Contains(record.GrantedPermissions, permission) {
					allowed = false
					break
				}
			}
			if allowed {
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
