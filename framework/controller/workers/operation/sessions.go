package operation

import (
	"context"
	"fmt"

	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

// Operation persistence determines command acceptance. Session reconciliation
// follows that commit and can retry independently without rejecting accepted work.
func (r *Worker) observeAccepted(ctx context.Context, state *operationState) {
	if err := r.reconcileSessions(ctx, state); err != nil {
		r.logger.Warn("Accepted operation awaiting session reconciliation", "operation_id", state.operation.ID, "error_type", fmt.Sprintf("%T", err))
		r.signalScheduler()
	}
}

func (r *Worker) reconcileSessions(ctx context.Context, state *operationState) error {
	if r.sessions == nil {
		return nil
	}
	err := r.sessions.Observe(ctx, runtimeSessions.Evidence{OperationID: state.operation.ID})
	state.sessionPending = err != nil
	return err
}
