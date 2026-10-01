package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"commander/framework/comms"
	adapterevents "commander/framework/comms/clients/adapter/events"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/comms/messages"
	controllerconsumers "commander/framework/controller/consumers"
	"commander/framework/domain"
	"commander/framework/id"
)

// Consumer processes adapter commands independently of agent events. A bounded
// number of commands can wait for actions without blocking other commands or agent events.
type Consumer struct {
	bus           comms.Bus
	commands      comms.Subscription
	cancellations comms.Subscription
	controller    controllercommands.Controller
	operations    controllercommands.Operations
	logger        *slog.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	running       atomic.Bool
	ledger        requestLedger
	closeOnce     sync.Once
	closeErr      error
}

// NewConsumer subscribes before returning so a host can advertise readiness
// only after both commands and cancellation notices have a receiver.
func NewConsumer(ctx context.Context, bus comms.Bus, controllerType, controllerID string, controller controllercommands.Controller, operations controllercommands.Operations, logger *slog.Logger) (*Consumer, error) {
	if ctx == nil || bus == nil || controller == nil || operations == nil {
		return nil, errors.New("adapter consumer requires a bus, controller, and operations")
	}
	if err := messages.ValidateControllerType(controllerType); err != nil {
		return nil, err
	}
	if !id.ValidRuntime(controllerID, id.Controller) {
		return nil, errors.New("adapter consumer requires a valid controller process ID")
	}
	commands, err := bus.Subscribe(ctx, comms.Inbox{Topic: messages.ControllerCommands(controllerType), Name: controllerID, Mode: comms.TransientExclusive})
	if err != nil {
		return nil, err
	}
	cancellations, err := bus.Subscribe(ctx, comms.Inbox{Topic: messages.ControllerCancellations(controllerType), Name: controllerID, Mode: comms.TransientExclusive})
	if err != nil {
		return nil, errors.Join(err, commands.Close())
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &Consumer{ctx: lifetime, cancel: cancel, bus: bus, commands: commands, cancellations: cancellations, controller: controller, operations: operations, logger: logger, ledger: requestLedger{active: make(map[string]*requestState), canceled: make(map[string]string)}}, nil
}

func (c *Consumer) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		c.closeErr = errors.Join(c.commands.Close(), c.cancellations.Close())
	})
	return c.closeErr
}

// Consume may be restarted after it returns; completed replies stay cached.
// Only this goroutine accesses the ledger. The two bus consumers join their
// workers before returning, including workers finishing during cancellation.
func (c *Consumer) Consume(ctx context.Context) error {
	if !c.running.CompareAndSwap(false, true) {
		return errors.New("adapter consumer is already running")
	}
	defer c.running.Store(false)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	if c.ctx.Err() != nil {
		cancel()
	}
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

func (c *Consumer) consumeRequest(ctx context.Context, coordinator requestCoordinator, m messages.Message) error {
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

func (c *Consumer) consumeCancellation(ctx context.Context, coordinator requestCoordinator, m messages.Message) error {
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

func (c *Consumer) dispatch(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
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
