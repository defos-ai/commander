package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"commander/framework/comms"
	adapterevents "commander/framework/comms/clients/adapter/events"
	controllercommands "commander/framework/comms/clients/controller/commands"
	"commander/framework/comms/messages"
	controllerconsumers "commander/framework/controller/consumers"
	"commander/framework/domain"
	"commander/framework/id"
)

type requestState struct {
	message messages.Message
	done    chan struct{}
	cancel  context.CancelFunc
	reply   messages.Message
}

// Consumer processes adapter requests independently of agent events. A bounded
// number of requests can wait for actions without blocking other commands or agent events.
type Consumer struct {
	bus           comms.Bus
	requests      comms.Subscription
	cancellations comms.Subscription
	controller    controllercommands.Controller
	operations    controllercommands.Operations
	logger        *slog.Logger
	mu            sync.Mutex
	active        map[string]*requestState
	canceled      map[string]string
	completed     []string
	closeOnce     sync.Once
	closeErr      error
}

// NewConsumer subscribes before returning so a host can advertise readiness
// only after both requests and cancellation notices have a receiver.
func NewConsumer(ctx context.Context, bus comms.Bus, controllerType string, controller controllercommands.Controller, operations controllercommands.Operations, logger *slog.Logger) (*Consumer, error) {
	if bus == nil || controller == nil || operations == nil {
		return nil, errors.New("adapter consumer requires a bus, controller, and operations")
	}
	if err := messages.ValidateControllerType(controllerType); err != nil {
		return nil, err
	}
	requests, err := bus.Subscribe(ctx, messages.ControllerRequests(controllerType))
	if err != nil {
		return nil, err
	}
	cancellations, err := bus.Subscribe(ctx, messages.ControllerCancellations(controllerType))
	if err != nil {
		return nil, errors.Join(err, requests.Close())
	}
	return &Consumer{bus: bus, requests: requests, cancellations: cancellations, controller: controller, operations: operations, logger: logger, active: make(map[string]*requestState), canceled: make(map[string]string)}, nil
}

func (c *Consumer) Close() error {
	c.closeOnce.Do(func() { c.closeErr = errors.Join(c.requests.Close(), c.cancellations.Close()) })
	return c.closeErr
}

func (c *Consumer) Consume(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan error, 2)
	go func() {
		consumer := comms.Consumer{Subscription: c.requests, Logger: c.logger, Concurrency: 16, ContinueOnError: continueAfterInvalidInput}
		finished <- consumer.Consume(ctx, c.consumeRequest)
	}()
	go func() {
		consumer := comms.Consumer{Subscription: c.cancellations, Logger: c.logger, ContinueOnError: continueAfterInvalidInput}
		finished <- consumer.Consume(ctx, c.consumeCancellation)
	}()
	err := <-finished
	cancel()
	other := <-finished
	if errors.Is(other, context.Canceled) {
		other = nil
	}
	return errors.Join(err, other)
}

func continueAfterInvalidInput(_ messages.Message, err error) bool {
	return errors.Is(err, controllerconsumers.ErrInvalidMessage)
}

func (c *Consumer) consumeRequest(ctx context.Context, m messages.Message) error {
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
	c.mu.Lock()
	if previous := c.active[m.ID]; previous != nil {
		c.mu.Unlock()
		if previous.message.AdapterID != m.AdapterID || !bytes.Equal(previous.message.Payload, m.Payload) {
			return controllerconsumers.InvalidMessage(errors.New("request ID reused with different parameters"))
		}
		select {
		case <-previous.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		return (adapterevents.Client{Bus: c.bus}).Publish(ctx, previous.reply)
	}
	requestCtx, cancel := context.WithDeadline(ctx, envelope.Deadline)
	state := &requestState{message: m, done: make(chan struct{}), cancel: cancel}
	c.active[m.ID] = state
	if c.canceled[m.ID] == m.AdapterID {
		cancel()
	}
	delete(c.canceled, m.ID)
	c.mu.Unlock()
	defer cancel()
	var value any
	err := requestCtx.Err()
	if err == nil {
		value, err = c.dispatch(requestCtx, envelope.Method, envelope.Parameters)
	}
	reply, err := adapterevents.NewReply(m.AdapterID, m.ID, value, err)
	if err != nil {
		return err
	}
	c.mu.Lock()
	state.reply = reply
	state.cancel = nil
	close(state.done)
	c.completed = append(c.completed, m.ID)
	if len(c.completed) > 4096 {
		delete(c.active, c.completed[0])
		c.completed = c.completed[1:]
	}
	c.mu.Unlock()
	// Publishing the outcome finishes request processing, including domain errors.
	// Keep the cached reply if publication fails; redelivery must not repeat work.
	return (adapterevents.Client{Bus: c.bus}).Publish(ctx, reply)
}

func (c *Consumer) consumeCancellation(_ context.Context, m messages.Message) error {
	if err := m.Validate(); err != nil {
		return controllerconsumers.InvalidMessage(err)
	}
	if m.Type != controllercommands.AdapterRequestCanceled || !id.Valid(m.CorrelationID) {
		return controllerconsumers.InvalidMessage(errors.New("invalid adapter cancellation"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if state := c.active[m.CorrelationID]; state != nil {
		if state.message.AdapterID == m.AdapterID && state.cancel != nil {
			state.cancel()
		}
		return nil
	}
	// Cancellation may overtake a request waiting for a worker. Bound these
	// notices; the request's own deadline remains the fallback in every case.
	if len(c.canceled) >= 4096 {
		for requestID := range c.canceled {
			delete(c.canceled, requestID)
			break
		}
	}
	c.canceled[m.CorrelationID] = m.AdapterID
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
