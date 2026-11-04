package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
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
	Executions        executions.ExecutionStore
	Operations        operations.OperationStore
	Decryptor         consumers.SecretDecryptor
	Coordination      Coordinator
	ReconcileInterval time.Duration
}

type Worker struct {
	lifecycle         *workers.Lifecycle
	consumer          consumers.SecretEventConsumer
	coordination      Coordinator
	reconcileInterval time.Duration
	handlers          sync.Mutex
	closed            bool
	closeOnce         sync.Once
	closedDone        chan struct{}
}

func (*Worker) String() string   { return "secret worker (values redacted)" }
func (*Worker) GoString() string { return "secret worker (values redacted)" }

func New(deps Dependencies) (*Worker, error) {
	if deps.Secrets == nil || deps.Requests == nil {
		return nil, errors.New("secret worker requires both secret and request stores")
	}
	if deps.Bus == nil || deps.Inventory == nil || deps.Coordination == nil || deps.Executions == nil || deps.Operations == nil {
		return nil, errors.New("secret worker requires bus, inventory, execution and operation stores, and coordination")
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
			Executions: deps.Executions, Operations: deps.Operations,
		},
		coordination: deps.Coordination, reconcileInterval: deps.ReconcileInterval,
		closedDone: make(chan struct{}),
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	return w.lifecycle.Run(ctx, w.run)
}

func (w *Worker) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("worker shutdown requires a context")
	}
	w.lifecycle.Cancel()
	w.closeOnce.Do(func() {
		go func() {
			_ = w.lifecycle.Close(context.Background())
			w.handlers.Lock()
			w.closed = true
			w.consumer.ClearTransient()
			w.handlers.Unlock()
			close(w.closedDone)
		}()
	})
	select {
	case <-w.closedDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	w.handlers.Lock()
	defer w.handlers.Unlock()
	if w.closed {
		return domain.ErrControllerClosed
	}
	if err := w.consumer.Reconcile(ctx); err != nil {
		return fmt.Errorf("reconcile secret delivery: %w", err)
	}
	return nil
}

// Consume is called by the event worker inside the coordination owner callback.
func (w *Worker) Consume(ctx context.Context, message messages.Message) error {
	w.handlers.Lock()
	defer w.handlers.Unlock()
	if w.closed {
		return domain.ErrControllerClosed
	}
	return w.consumer.Consume(ctx, message)
}

func (w *Worker) InitializeTransient(ctx context.Context) error {
	return w.consumer.InitializeTransient(ctx)
}

func (w *Worker) ProvideTransient(ctx context.Context, requestID string, value []byte) error {
	var buffers sync.Mutex
	submission := bytes.Clone(value)
	defer func() {
		buffers.Lock()
		clear(submission)
		submission = nil
		buffers.Unlock()
	}()
	return w.coordination.Apply(ctx, func(ctx context.Context, _ *state.State) error {
		buffers.Lock()
		owned := bytes.Clone(submission)
		buffers.Unlock()
		defer clear(owned)
		if err := ctx.Err(); err != nil {
			return err
		}
		w.handlers.Lock()
		defer w.handlers.Unlock()
		if w.closed {
			return domain.ErrControllerClosed
		}
		return w.consumer.ProvideTransient(ctx, requestID, owned)
	})
}
