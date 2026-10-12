package runner

import (
	"context"
	"time"

	"github.com/defos-ai/commander/framework/domain/reports"
)

// publishFinalizations runs in the owner loop, including after startup recovery.
// The saved finalizer task supplies a stable report identity and timestamp so a
// failed publication or operation write can retry without producing new content.
func (r *Runner) publishFinalizations() {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()
	for _, state := range r.states {
		if active(state) != nil {
			continue
		}
		output, err := state.operation.FinalizationOutput()
		if err != nil {
			continue
		}
		i := taskIndex(&state.operation, state.operation.FinalizationTaskID)
		task := state.operation.Tasks[i]
		report := reports.Report{ID: task.ID, OperationID: state.operation.ID, Mission: state.operation.Mission.Name,
			Output: output, CreatedAt: task.FinishedAt.UTC()}
		if err := r.reports.Put(ctx, report); err != nil {
			r.logger.Warn("Automatic report publication deferred", "operation_id", state.operation.ID, "error", err)
			continue
		}
		// Complete atomically rechecks this finalizer's output and settled graph.
		next, err := r.store.Complete(ctx, state.operation.ID, report.ID, output)
		if err := state.acceptUpdate(next, err); err != nil {
			r.logger.Warn("Automatic operation completion deferred", "operation_id", state.operation.ID, "error", err)
		}
	}
}
