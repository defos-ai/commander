package action

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/defos-ai/commander/framework/domain/executions"
)

var (
	ErrCheckpointsUnavailable = errors.New("checkpoints require an active controller-dispatched execution")
	ErrCheckpointRejected     = errors.New("checkpoint request was rejected")
)

// CheckpointProvider is scoped to one execution by the agent runtime. It never
// accepts caller-supplied agent, task or command identities.
type CheckpointProvider interface {
	LoadCheckpoint(context.Context, string) (json.RawMessage, bool, error)
	SaveCheckpoint(context.Context, string, json.RawMessage) error
}

type checkpointContextKey struct{}
type checkpointContext struct{ provider CheckpointProvider }

// WithCheckpoints injects an execution-scoped provider. Agent.Serve supplies it
// automatically; nil removes any inherited provider.
func WithCheckpoints(ctx context.Context, provider CheckpointProvider) context.Context {
	return context.WithValue(ctx, checkpointContextKey{}, checkpointContext{provider})
}

// LoadCheckpoint reads a named fact saved for this execution. A missing name
// returns found=false. Callers own returned bytes; checkpoints contain no secrets.
func LoadCheckpoint(ctx context.Context, name string) (data json.RawMessage, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := executions.ValidateCheckpoint(name, json.RawMessage(`{}`)); err != nil {
		return nil, false, err
	}
	scoped, _ := ctx.Value(checkpointContextKey{}).(checkpointContext)
	if scoped.provider == nil {
		return nil, false, ErrCheckpointsUnavailable
	}
	data, found, err = scoped.provider.LoadCheckpoint(ctx, name)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	if err := executions.ValidateCheckpoint(name, data); err != nil {
		return nil, false, ErrCheckpointRejected
	}
	return bytes.Clone(data), true, nil
}

// SaveCheckpoint waits for controller persistence. Identical writes are safe to
// retry; an error or cancellation may leave the write outcome unknown. It never
// authorizes repeating an external side effect. Do not save secrets or files.
func SaveCheckpoint(ctx context.Context, name string, data json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := executions.ValidateCheckpoint(name, data); err != nil {
		return err
	}
	scoped, _ := ctx.Value(checkpointContextKey{}).(checkpointContext)
	if scoped.provider == nil {
		return ErrCheckpointsUnavailable
	}
	if err := scoped.provider.SaveCheckpoint(ctx, name, bytes.Clone(data)); err != nil {
		return err
	}
	return ctx.Err()
}
