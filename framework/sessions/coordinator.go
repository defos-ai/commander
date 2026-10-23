// Package sessions coordinates durable lifecycle, dispatch and execution claims.
package sessions

import (
	"context"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

type Coordinator struct {
	admitted   map[string]bool
	bound      map[string]string
	observed   map[model.Reference]uint64
	store      model.Store
	operations operations.OperationStore
	executions executions.ExecutionStore
	agents     agents.AgentStore
	actions    actions.ActionStore
	remote     func(context.Context, model.Control) (model.Session, error)
	gate       chan struct{}
}

func New(store model.Store, ops operations.OperationStore, executions executions.ExecutionStore, agents agents.AgentStore, catalog actions.ActionStore, remote func(context.Context, model.Control) (model.Session, error)) (*Coordinator, error) {
	if store == nil || ops == nil || executions == nil || agents == nil || catalog == nil || remote == nil {
		return nil, errors.New("runtime sessions require stores and an owner control transport")
	}
	return &Coordinator{store: store, operations: ops, executions: executions, agents: agents, actions: catalog, remote: remote, gate: make(chan struct{}, 1), admitted: map[string]bool{}, bound: map[string]string{}, observed: map[model.Reference]uint64{}}, nil
}
func (c *Coordinator) lock(ctx context.Context) error {
	select {
	case c.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *Coordinator) unlock() { <-c.gate }
func (c *Coordinator) apply(ctx context.Context, change model.Change) (model.Record, error) {
	change.At = time.Now().UTC()
	return c.store.Apply(ctx, change)
}
