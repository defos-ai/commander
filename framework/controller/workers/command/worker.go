package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/defos-ai/commander/framework/comms"
	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	controllercommands "github.com/defos-ai/commander/framework/comms/clients/controller/commands"
	"github.com/defos-ai/commander/framework/comms/messages"
	controllerconsumers "github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/controller/workers"
	"github.com/defos-ai/commander/framework/domain"
	"github.com/defos-ai/commander/framework/domain/sessions"
	"github.com/defos-ai/commander/framework/id"
)

type Dependencies struct {
	Bus            comms.Bus
	ControllerType string
	ControllerID   string
	Controller     controllercommands.Controller
	Operations     controllercommands.Operations
	Logger         *slog.Logger
}

type Worker struct {
	lifecycle     *workers.Lifecycle
	bus           comms.Bus
	commands      comms.Subscription
	cancellations comms.Subscription
	controller    controllercommands.Controller
	operations    controllercommands.Operations
	logger        *slog.Logger
	ledger        requestLedger
	closeOnce     sync.Once
	cleanupDone   chan struct{}
	closeErr      error // written before cleanupDone closes
}

func New(ctx context.Context, deps Dependencies) (*Worker, error) {
	if ctx == nil || deps.Bus == nil || deps.Controller == nil || deps.Operations == nil {
		return nil, errors.New("command worker requires a context, bus, controller and operations")
	}
	if err := messages.ValidateControllerType(deps.ControllerType); err != nil {
		return nil, err
	}
	if !id.ValidRuntime(deps.ControllerID, id.Controller) {
		return nil, errors.New("command worker requires a valid controller process ID")
	}
	lifetime := workers.NewLifecycle(ctx)
	commands, err := deps.Bus.Subscribe(lifetime.Context(), comms.Inbox{Topic: messages.ControllerCommands(deps.ControllerType), Name: deps.ControllerID, Mode: comms.TransientExclusive})
	if err != nil {
		lifetime.Cancel()
		return nil, err
	}
	cancellations, err := deps.Bus.Subscribe(lifetime.Context(), comms.Inbox{Topic: messages.ControllerCancellations(deps.ControllerType), Name: deps.ControllerID, Mode: comms.TransientExclusive})
	if err != nil {
		lifetime.Cancel()
		return nil, errors.Join(err, commands.Close())
	}
	return &Worker{
		lifecycle: lifetime, bus: deps.Bus, commands: commands, cancellations: cancellations,
		controller: deps.Controller, operations: deps.Operations, logger: deps.Logger,
		ledger:      requestLedger{active: make(map[string]*requestState), canceled: make(map[string]string)},
		cleanupDone: make(chan struct{}),
	}, nil
}

func (c *Worker) Close(ctx context.Context) error {
	c.lifecycle.Cancel()
	c.closeOnce.Do(func() {
		go func() {
			defer close(c.cleanupDone)
			commandsDone, cancellationsDone := make(chan error, 1), make(chan error, 1)
			go func() {
				err := c.commands.Close()
				if errors.Is(err, comms.ErrClosed) {
					err = nil
				}
				commandsDone <- err
			}()
			go func() {
				err := c.cancellations.Close()
				if errors.Is(err, comms.ErrClosed) {
					err = nil
				}
				cancellationsDone <- err
			}()
			c.closeErr = errors.Join(<-commandsDone, <-cancellationsDone)
		}()
	})
	if err := c.lifecycle.Close(ctx); err != nil {
		return err
	}
	select {
	case <-c.cleanupDone:
		return c.closeErr
	default:
	}
	select {
	case <-c.cleanupDone:
		return c.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Worker) Run(ctx context.Context) error {
	return c.lifecycle.Run(ctx, c.run)
}

// This loop owns the ledger until both consumers have joined their requests.
func (c *Worker) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	coordinator := requestCoordinator{claims: make(chan requestClaim), completions: make(chan requestCompletion), cancellations: make(chan requestCancellation)}
	finished := make(chan error, 2)
	go func() {
		consumer := comms.Consumer{Subscription: c.commands, Logger: c.logger, Concurrency: 16, ContinueOnError: continueAfterInvalidInput}
		finished <- consumer.Consume(ctx, func(ctx context.Context, m messages.Message) error { return c.consumeRequest(ctx, coordinator, m) })
	}()
	go func() {
		consumer := comms.Consumer{Subscription: c.cancellations, Logger: c.logger, ContinueOnError: continueAfterInvalidInput}
		finished <- consumer.Consume(ctx, func(ctx context.Context, m messages.Message) error { return c.consumeCancellation(ctx, coordinator, m) })
	}()
	var result error
	for remaining := 2; remaining > 0; {
		select {
		case claim := <-coordinator.claims:
			c.ledger.claim(claim)
		case completion := <-coordinator.completions:
			c.ledger.complete(completion)
		case cancellation := <-coordinator.cancellations:
			c.ledger.cancel(cancellation)
		case err := <-finished:
			if remaining == 2 || !errors.Is(err, context.Canceled) {
				result = errors.Join(result, err)
			}
			remaining--
			cancel()
		}
	}
	return result
}

func continueAfterInvalidInput(_ messages.Message, err error) bool {
	return errors.Is(err, controllerconsumers.ErrInvalidMessage)
}

func (c *Worker) consumeRequest(ctx context.Context, coordinator requestCoordinator, m messages.Message) error {
	if err := m.Validate(); err != nil {
		return controllerconsumers.InvalidMessage(err)
	}
	if m.Type != controllercommands.AdapterRequest {
		return controllerconsumers.InvalidMessage(errors.New("unexpected adapter request type"))
	}
	var envelope controllercommands.CommandEnvelope
	if err := json.Unmarshal(m.Payload, &envelope); err != nil {
		return controllerconsumers.InvalidMessage(err)
	}
	if err := envelope.Validate(); err != nil {
		return controllerconsumers.InvalidMessage(err)
	}
	if envelope.Method == controllercommands.MethodProvideTransientSecret {
		return c.consumeTransient(ctx, m, envelope)
	}
	claimed := coordinator.claim(ctx, m, envelope.Deadline)
	if claimed.err != nil {
		return claimed.err
	}
	state := claimed.state
	if claimed.execute {
		var value any
		err := state.ctx.Err()
		if err == nil {
			value, err = c.dispatch(state.ctx, envelope.Method, envelope.Parameters)
		}
		reply, err := adapterevents.NewReply(m.AdapterID, m.ID, value, err)
		coordinator.completions <- requestCompletion{state: state, reply: reply, err: err}
		<-state.done
	} else {
		select {
		case <-state.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if state.err != nil {
		return state.err
	}
	// A failed publication retains the cached reply; redelivery must not repeat work.
	return (adapterevents.Client{Bus: c.bus}).Publish(ctx, state.reply)
}

func (c *Worker) consumeCancellation(ctx context.Context, coordinator requestCoordinator, m messages.Message) error {
	if err := m.Validate(); err != nil {
		return controllerconsumers.InvalidMessage(err)
	}
	if m.Type != controllercommands.AdapterRequestCanceled || !id.Valid(m.CorrelationID) {
		return controllerconsumers.InvalidMessage(errors.New("invalid adapter cancellation"))
	}
	cancellation := requestCancellation{message: m, done: make(chan struct{})}
	select {
	case coordinator.cancellations <- cancellation:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-cancellation.done
	return nil
}

func (c *Worker) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case controllercommands.MethodProvideTransientSecret:
		return struct{}{}, c.provideTransient(ctx, params)
	case controllercommands.MethodControlSession:
		var p sessions.Control
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		target, ok := c.controller.(controllercommands.SessionController)
		if !ok {
			return nil, errors.New("controller does not support sessions")
		}
		return target.ControlSession(ctx, p)
	case controllercommands.MethodDiscoverActions:
		var p controllercommands.DiscoverActionsCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.controller.DiscoverActions(ctx, p.AgentID, p.Scope)
	case controllercommands.MethodRunAction:
		var p controllercommands.RunActionCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.controller.RunAction(ctx, p.AgentID, p.Call)
	case controllercommands.MethodStopAgent:
		var p controllercommands.StopAgentCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.controller.StopAgent(ctx, p.AgentID)
	case controllercommands.MethodStartOperation:
		var p controllercommands.StartOperationCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Start(ctx, p)
	case controllercommands.MethodResolveContinuation:
		var p controllercommands.ResolveContinuationCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Resolve(ctx, p)
	case controllercommands.MethodAssignTask:
		var p controllercommands.AssignTaskCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Assign(ctx, p)
	case controllercommands.MethodAskQuestion:
		var p controllercommands.AskQuestionCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Ask(ctx, p)
	case controllercommands.MethodAnswerQuestion:
		var p controllercommands.AnswerQuestionCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Answer(ctx, p)
	case controllercommands.MethodCompleteOperation:
		var p controllercommands.CompleteOperationCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Complete(ctx, p)
	case controllercommands.MethodFailOperation:
		var p controllercommands.FailOperationCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Fail(ctx, p)
	case controllercommands.MethodCancelOperation:
		var p controllercommands.CancelOperationCommand
		if err := domain.Decode(params, &p); err != nil {
			return nil, err
		}
		return c.operations.Cancel(ctx, p)
	default:
		return nil, fmt.Errorf("unknown controller request method %q", method)
	}
}
