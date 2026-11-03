package event

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/defos-ai/commander/framework/comms"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/domain/actions"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/artifacts"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/internal/state"
	runtimeSessions "github.com/defos-ai/commander/framework/sessions"
)

type SessionObserver interface {
	Observe(context.Context, runtimeSessions.Evidence) error
}

type ResultSharing interface {
	QueueResult(messages.Message)
}

type Coordinator interface {
	Apply(context.Context, func(context.Context, *state.State) error) error
	Stop(error)
}

type Dependencies struct {
	Bus            comms.Bus
	ControllerType string
	ControllerID   string
	Inventory      agents.AgentStore
	Actions        actions.ActionStore
	Executions     executions.ExecutionStore
	Operations     operations.OperationStore
	Artifacts      artifacts.ArtifactStore
	Secrets        consumers.SecretEvents
	Sessions       SessionObserver
	Sharing        ResultSharing
	Coordination   Coordinator
	Logger         *slog.Logger
}

type Worker struct {
	lifecycle    *workers.Lifecycle
	subscription comms.Subscription
	consumer     consumers.EventConsumer
	coordination Coordinator
	sessions     SessionObserver
	sharing      ResultSharing
	logger       *slog.Logger
	closeOnce    sync.Once
	cleanupDone  chan struct{}
	closeErr     error // written before cleanupDone closes
}

func New(ctx context.Context, deps Dependencies) (*Worker, error) {
	if deps.Bus == nil || deps.Coordination == nil || deps.Logger == nil {
		return nil, errors.New("events require a bus, coordination and logger")
	}
	lifetime := workers.NewLifecycle(ctx)
	subscription, err := deps.Bus.Subscribe(lifetime.Context(), comms.Inbox{
		Topic: messages.ControllerEvents(deps.ControllerType), Name: deps.ControllerID, Mode: comms.TransientExclusive,
	})
	if err != nil {
		lifetime.Cancel()
		return nil, err
	}
	return &Worker{
		lifecycle: lifetime, cleanupDone: make(chan struct{}),
		subscription: subscription, coordination: deps.Coordination, sessions: deps.Sessions, sharing: deps.Sharing, logger: deps.Logger,
		consumer: consumers.EventConsumer{
			Bus: deps.Bus, Inventory: deps.Inventory, Actions: deps.Actions, Executions: deps.Executions,
			Operations: deps.Operations, Artifacts: deps.Artifacts, Secrets: deps.Secrets,
		},
	}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	return w.lifecycle.Run(ctx, w.run)
}

func (w *Worker) run(ctx context.Context) error {
	consumer := comms.Consumer{
		Subscription: w.subscription, Logger: w.logger,
		ContinueOnError: recoverableEvent,
	}
	return consumer.Consume(ctx, func(ctx context.Context, message messages.Message) error {
		return w.coordination.Apply(ctx, func(ctx context.Context, owner *state.State) error {
			handler := w.consumer
			handler.State = owner
			err := handler.Consume(ctx, message)
			if err == nil && w.sharing != nil {
				w.sharing.QueueResult(message)
			}
			if err == nil && w.sessions != nil && agentObservation(message.Type) {
				err = w.sessions.Observe(ctx, runtimeSessions.Evidence{AgentID: message.AgentID})
			}
			// Fence new commands before releasing a failed observation from the owner loop.
			if err != nil && !recoverableEvent(message, err) {
				w.coordination.Stop(err)
			}
			return err
		})
	})
}

func (w *Worker) Close(ctx context.Context) error {
	w.lifecycle.Cancel()
	// Cleanup must also run when Consume is blocked waiting on the subscription.
	w.closeOnce.Do(func() {
		go func() {
			defer close(w.cleanupDone)
			if err := w.subscription.Close(); !errors.Is(err, comms.ErrClosed) {
				w.closeErr = err
			}
		}()
	})
	if err := w.lifecycle.Close(ctx); err != nil {
		return err
	}
	select {
	case <-w.cleanupDone:
		return w.closeErr
	default:
	}
	select {
	case <-w.cleanupDone:
		return w.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func recoverableEvent(message messages.Message, err error) bool {
	return message.Type == messages.ExecutionLogged || errors.Is(err, consumers.ErrInvalidMessage)
}

func agentObservation(kind messages.MessageType) bool {
	return kind == messages.AgentStarted || kind == messages.AgentUpdated || kind == messages.AgentHeartbeat || kind == messages.AgentStopped
}
