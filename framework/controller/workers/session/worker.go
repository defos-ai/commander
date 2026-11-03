package session

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	model "github.com/defos-ai/commander/framework/domain/sessions"
)

type OwnerControl interface {
	ControlSessionRemote(context.Context, model.Control) (model.Session, error)
}

type Dependencies struct {
	Store      model.Store
	Operations operations.OperationStore
	Executions executions.ExecutionStore
	Agents     agents.AgentStore
	Actions    actions.ActionStore
	Remote     OwnerControl
	Logger     *slog.Logger
}

type Worker struct {
	admitted   map[string]bool
	bound      map[string]string
	observed   map[model.Reference]uint64
	store      model.Store
	operations operations.OperationStore
	executions executions.ExecutionStore
	agents     agents.AgentStore
	actions    actions.ActionStore
	remote     OwnerControl
	logger     *slog.Logger
	gate       chan struct{}
}

func New(dependencies Dependencies) (*Worker, error) {
	if dependencies.Store == nil || dependencies.Operations == nil || dependencies.Executions == nil || dependencies.Agents == nil || dependencies.Actions == nil || dependencies.Remote == nil {
		return nil, errors.New("runtime sessions require stores and an owner control transport")
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default()
	}
	return &Worker{
		store: dependencies.Store, operations: dependencies.Operations, executions: dependencies.Executions,
		agents: dependencies.Agents, actions: dependencies.Actions, remote: dependencies.Remote, logger: dependencies.Logger,
		gate: make(chan struct{}, 1), admitted: map[string]bool{}, bound: map[string]string{}, observed: map[model.Reference]uint64{},
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if err := w.ResumeControls(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
			w.logger.Warn("Reconcile session controls", "error", err)
		}
	}
}

func (w *Worker) lock(ctx context.Context) error {
	select {
	case w.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) unlock() { <-w.gate }

func (w *Worker) apply(ctx context.Context, change model.Change) (model.Record, error) {
	change.At = time.Now().UTC()
	return w.store.Apply(ctx, change)
}
