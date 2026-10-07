package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	adapterevents "github.com/defos-ai/commander/framework/comms/clients/adapter/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/id"
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
	register     chan pendingCall
	release      chan string
	replies      chan correlatedReply
	consumerDone chan struct{}
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
	subscription, err := bus.Subscribe(ctx, comms.Inbox{Topic: messages.AdapterReplies(adapterID), Name: adapterID, Mode: comms.TransientExclusive})
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancelCause(ctx)
	c := &Client{bus: bus, adapterID: adapterID, subscription: subscription, ctx: lifetime, cancel: cancel,
		done: make(chan struct{}), consumerDone: make(chan struct{}), register: make(chan pendingCall),
		release: make(chan string), replies: make(chan correlatedReply)}
	go c.run()
	go c.consumeReplies(logger)

	return c, nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.cancel(comms.ErrClosed)
		c.closeErr = c.subscription.Close()
		<-c.consumerDone
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
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	if c.ctx.Err() != nil {
		cancel()
	}
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
	select {
	case c.register <- pendingCall{id: m.ID, reply: waiting}:
	case <-ctx.Done():
		return c.callError(ctx)
	case <-c.ctx.Done():
		return context.Cause(c.ctx)
	}
	defer func() {
		select {
		case c.release <- m.ID:
		case <-c.ctx.Done():
		}
	}()

	if err := c.bus.Publish(ctx, messages.ControllerCommands(messages.DefaultControllerType), m); err != nil {
		if ctx.Err() != nil {
			c.cancelRequest(m.ID)
			err = c.callError(ctx)
		}
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
		return c.callError(ctx)
	case <-c.ctx.Done():
		c.cancelRequest(m.ID)
		return context.Cause(c.ctx)
	}
}

func (c *Client) callError(ctx context.Context) error {
	if err := context.Cause(c.ctx); err != nil {
		return err
	}
	return ctx.Err()
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
