package runner_test

import (
	"context"
	"sync/atomic"
	"testing"

	"commander/framework/domain/actions"
	"commander/framework/domain/executions"
	"commander/framework/domain/operations"
)

func TestRecoveryObservesSavedResultWithoutNewExecution(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "running at shutdown"
		if interrupted {
			name = "interrupted after lost result receipt"
		}
		t.Run(name, func(t *testing.T) {
			config := fixture()
			started := make(chan invocation, 1)
			var dispatches atomic.Int32
			e := executor{run: func(ctx context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				dispatches.Add(1)
				started <- invocation{agent: agent, call: call}
				if interrupted {
					return actions.ActionOutput{}, executions.ErrIndeterminate
				}
				<-ctx.Done()
				return actions.ActionOutput{}, ctx.Err()
			}}
			first := newRunner(t, config, e)
			op := start(t, first)
			task := assign(t, first, op.ID, "inspect")
			original := receive(t, started)
			if interrupted {
				awaitOperation(t, first, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskInterrupted })
			}
			closeRunner(t, first)
			observed := make(chan invocation, 1)
			e.recover = func(_ context.Context, agent string, call actions.ActionCall) (actions.ActionOutput, error) {
				observed <- invocation{agent: agent, call: call}
				return output(call.ActionName), nil
			}

			restarted := newRunner(t, config, e)
			recovered := receive(t, observed)
			settled := awaitOperation(t, restarted, op.ID, func(op operations.Operation) bool { return op.Tasks[0].Status == operations.TaskSucceeded })

			if dispatches.Load() != 1 || recovered.agent != original.agent || recovered.call.Work != original.call.Work {
				t.Fatalf("recovery repeated or changed work: dispatches=%d, recovered=%+v", dispatches.Load(), recovered)
			}
			got := settled.Tasks[0]
			if len(settled.Tasks) != 1 || got.ID != task.ID || got.CommandID != task.ID || got.Result == nil || got.Error != "" {
				t.Fatalf("recovery lost successful outcome: %+v", got)
			}
			if settled.Status != operations.Planning || got.Continuation.DecisionBy != operations.DecisionModel || got.Continuation.Status != operations.ContinuationPending {
				t.Fatalf("recovery did not return control to the model: %+v", settled)
			}
		})
	}
}
