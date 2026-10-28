package runner

import (
	"context"
	"time"

	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/operations"
)

const (
	taskObservationTimeout  = 5 * time.Second
	taskObservationInterval = time.Second
	maxTaskObservers        = 8
)

type taskObservation struct {
	inFlight    bool
	nextAttempt time.Time
}

// observeInterruptedTasks checks original executions independently of agent
// readiness and operation completion. It never authorizes a new dispatch.
func (r *Runner) observeInterruptedTasks() {
	if r.ctx.Err() != nil {
		return
	}
	if r.observations == nil {
		r.observations = make(map[string]taskObservation)
	}
	observers := 0
	for _, observation := range r.observations {
		if observation.inFlight {
			observers++
		}
	}
	now := time.Now()
	for _, state := range r.states {
		if state.err != nil {
			continue
		}
		for _, task := range state.operation.Tasks {
			if observers >= maxTaskObservers {
				return
			}
			if task.Status != operations.TaskInterrupted {
				continue
			}
			if _, pending := r.pendingResults[task.ID]; pending {
				continue
			}
			observation := r.observations[task.ID]
			if observation.inFlight || now.Before(observation.nextAttempt) {
				continue
			}
			r.observations[task.ID] = taskObservation{inFlight: true}
			observers++
			r.workers++
			go r.observeTask(task.Clone())
		}
	}
}

func (r *Runner) observeTask(task operations.Task) {
	ctx, cancel := context.WithTimeout(r.ctx, taskObservationTimeout)
	defer cancel()
	call := actions.ActionCall{ActionName: task.ActionName, Input: task.Input,
		Work: messages.WorkContext{OperationID: task.OperationID, TaskID: task.ID, StepID: task.StepID}}
	output, err := r.executor.RecoverAction(ctx, task.AgentID, call)
	r.results <- taskResult{task: task, output: output, err: err, observation: true}
}
