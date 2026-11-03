package secret

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/internal/state"
)

type Coordinator interface {
	Apply(context.Context, func(context.Context, *state.State) error) error
	Stop(error)
}

type Dependencies struct {
	Bus               comms.Bus
	Inventory         agents.AgentStore
	Secrets           secrets.SecretStore
	Requests          secrets.RequestStore
	Decryptor         consumers.SecretDecryptor
	Coordination      Coordinator
	ReconcileInterval time.Duration
}

type Worker struct {
	lifecycle         *workers.Lifecycle
	consumer          consumers.SecretEventConsumer
	coordination      Coordinator
	reconcileInterval time.Duration
}

func New(deps Dependencies) (*Worker, error) {
	if deps.Secrets == nil || deps.Requests == nil {
		return nil, errors.New("secret worker requires both secret and request stores")
	}
	if deps.Bus == nil || deps.Inventory == nil || deps.Coordination == nil {
		return nil, errors.New("secret worker requires bus, inventory and coordination")
	}
	if deps.ReconcileInterval < 0 {
		return nil, errors.New("secret reconcile interval must not be negative")
	}
	if deps.ReconcileInterval == 0 {
		deps.ReconcileInterval = time.Second
	}
	return &Worker{
		lifecycle: workers.NewLifecycle(context.Background()),
		consumer: consumers.SecretEventConsumer{
			Bus: deps.Bus, Inventory: deps.Inventory, Secrets: deps.Secrets,
			Requests: deps.Requests, Decryptor: deps.Decryptor,
		},
		coordination: deps.Coordination, reconcileInterval: deps.ReconcileInterval,
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	return w.lifecycle.Run(ctx, w.run)
}

func (w *Worker) Close(ctx context.Context) error {
	return w.lifecycle.Close(ctx)
}

func (w *Worker) run(ctx context.Context) error {
	ticker := time.NewTicker(w.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		err := w.coordination.Apply(ctx, func(ctx context.Context, _ *state.State) error {
			err := w.Reconcile(ctx)
			if err != nil {
				w.coordination.Stop(err)
			}
			return err
		})
		if err != nil {
			return err
		}
	}
}

// Reconcile runs directly before worker startup or within the coordination owner.
func (w *Worker) Reconcile(ctx context.Context) error {
	if err := w.consumer.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile secret delivery: %w", err)
	}
	return nil
}

// Consume is called by the event worker inside the coordination owner callback.
func (w *Worker) Consume(ctx context.Context, message messages.Message) error {
	return w.consumer.Consume(ctx, message)
}
