package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"commander/framework/comms"
	adapterevents "commander/framework/comms/clients/adapter/events"
	"commander/framework/comms/messages"
	"commander/framework/id"
)

// Client owns controller routing and one reply subscription. Calls use independent correlations and
// never retry an action automatically. A timeout may follow an accepted action;
// callers should query the operation before deciding to submit more work.
type Client struct {
	bus          comms.Bus
	adapterID    string
	subscription comms.Subscription
	ctx          context.Context
	cancel       context.CancelCauseFunc
	done         chan struct{}
	mu           sync.Mutex
	pending      map[string]chan adapterevents.ControllerReply
	closeOnce    sync.Once
	closeErr     error
}

func NewClient(ctx context.Context, bus comms.Bus, adapterID string, logger *slog.Logger) (*Client, error) {
	if ctx == nil || bus == nil {
		return nil, errors.New("controller client requires a context and bus")
	}
	if !id.ValidRuntime(adapterID, id.Adapter) {
		return nil, errors.New("controller client requires a valid adapter ID")
	}
	subscription, err := bus.Subscribe(ctx, messages.AdapterReplies(adapterID))
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancelCause(ctx)
	c := &Client{bus: bus, adapterID: adapterID, subscription: subscription, ctx: lifetime, cancel: cancel, done: make(chan struct{}), pending: make(map[string]chan adapterevents.ControllerReply)}
	go func() {
		defer close(c.done)
		consumer := comms.Consumer{Subscription: subscription, Logger: logger, ContinueOnError: func(_ messages.Message, err error) bool {
			return errors.Is(err, comms.ErrInvalidMessage)
		}}
		err := consumer.Consume(lifetime, func(_ context.Context, m messages.Message) error {
			if err := m.Validate(); err != nil {
				return comms.InvalidMessage(err)
			}
			if m.Type != adapterevents.ControllerResponse || m.AdapterID != adapterID {
				return comms.InvalidMessage(errors.New("unexpected controller reply"))
			}
			var reply adapterevents.ControllerReply
			if err := json.Unmarshal(m.Payload, &reply); err != nil {
				return comms.InvalidMessage(err)
			}
			if !id.Valid(m.CorrelationID) {
				return comms.InvalidMessage(errors.New("invalid controller reply correlation"))
			}
			if err := reply.Validate(); err != nil {
				return comms.InvalidMessage(err)
			}
			c.mu.Lock()
			waiting := c.pending[m.CorrelationID]
			c.mu.Unlock()
			if waiting != nil {
				select {
				case waiting <- reply:
				default:
				}
			}
			return nil
		})
		cancel(err)
	}()
	return c, nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.cancel(comms.ErrClosed)
		c.closeErr = c.subscription.Close()
		<-c.done
	})
	return c.closeErr
}

func (c *Client) call(ctx context.Context, method string, params interface{ Validate() error }, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := context.Cause(c.ctx); err != nil {
		return err
	}
	deadline, exists := ctx.Deadline()
	if !exists {
		deadline = time.Now().Add(5 * time.Minute)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := params.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(params)
	if err != nil {
		return err
	}
	m, err := messages.NewAdapterMessage(messages.AdapterRequest, c.adapterID, CommandEnvelope{Method: method, Parameters: data, Deadline: deadline})
	if err != nil {
		return err
	}
	waiting := make(chan adapterevents.ControllerReply, 1)
	c.mu.Lock()
	c.pending[m.ID] = waiting
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, m.ID); c.mu.Unlock() }()
	if err := c.bus.Publish(ctx, messages.ControllerRequests(messages.DefaultControllerType), m); err != nil {
		return fmt.Errorf("publish controller request: %w", err)
	}
	select {
	case reply := <-waiting:
		if reply.Error != nil {
			return adapterevents.DecodeError(reply.Error)
		}
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("decode controller response: %w", err)
		}
		return nil
	case <-ctx.Done():
		c.cancelRequest(m.ID)
		return ctx.Err()
	case <-c.ctx.Done():
		c.cancelRequest(m.ID)
		return context.Cause(c.ctx)
	}
}

func (c *Client) cancelRequest(requestID string) {
	m, err := messages.NewAdapterMessage(messages.AdapterRequestCanceled, c.adapterID, nil)
	if err != nil {
		return
	}
	m.CorrelationID = requestID
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.ctx), time.Second)
	defer cancel()
	_ = c.bus.Publish(ctx, messages.ControllerCancellations(messages.DefaultControllerType), m)
}
