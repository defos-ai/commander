package runner

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
	agentmodel "github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/missions"
	"github.com/defos-ai/commander/framework/domain/operations"
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
			agentID, err := r.selectAgent(ctx, s.operation, task, records, used)
			if err != nil && r.ctx.Err() == nil {
				r.logger.Warn("Agent selection failed", "operation_id", s.operation.ID, "task_id", task.ID, "selector", r.selector.Name(), "error", err)
			}
			if ctx.Err() != nil {
				return
			}
			if err != nil || agentID == "" {
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

func (r *Runner) selectAgent(ctx context.Context, operation operations.Operation, task operations.Task, records []agentmodel.Agent, used map[string]int) (string, error) {
	var affinityCandidates []string
	grouped := slices.Contains(operation.Mission.Rules[missions.SameAgent].ForActions, task.ActionName)
	if grouped {
		boundAgentID, err := operation.AffinityAgentID()
		if err != nil {
			return "", err
		}
		if boundAgentID != "" {
			// A preceding dispatch in this scheduler pass may have just bound
			// the group; the saved preflight can still list unbound candidates.
			affinityCandidates = []string{boundAgentID}
		} else if operation.Preflight != nil {
			for _, readiness := range operation.Preflight.Actions {
				if readiness.ActionName == task.ActionName {
					affinityCandidates = readiness.ReadyAgentIDs
					break
				}
			}
		}
	}
	selection := AgentSelection{PreviousAgentID: r.lastAgent}
	eligible := make(map[string]bool)
	for _, record := range records {
		if grouped && !slices.Contains(affinityCandidates, record.ID) {
			continue
		}
		if record.Stale || record.Status != agentmodel.AgentReady || used[record.ID] >= r.capacity {
			continue
		}
		for _, action := range record.Actions {
			if action.Name != task.ActionName {
				continue
			}
			if operations.AssessAgentAction(record, action).Reason == "" {
				eligible[record.ID] = true
				selection.Candidates = append(selection.Candidates, AgentCandidate{
					Agent: record.Clone(), RunningTasks: used[record.ID], Capacity: r.capacity,
				})
			}
			break
		}
	}
	if len(selection.Candidates) == 0 {
		return "", nil
	}
	selection.Task = task.Clone()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	agentID, err := r.selector.Select(ctx, selection)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if agentID != "" && !eligible[agentID] {
		return "", errors.New("agent selector returned an ineligible agent")
	}
	return agentID, nil
}
