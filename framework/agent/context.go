package agent

import (
	"context"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/consumers"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
)

const contextBufferSize = 32

type sharedContextClientKey struct{}

type sharedContextClient struct {
	agentID string
	service context.Context
	mu      sync.Mutex
	active  *executionContextInbox
}

type executionContextInbox struct {
	client    *sharedContextClient
	lifetime  context.Context
	commandID string
	work      messages.WorkContext
	deadline  time.Time
	updates   chan agentevents.ContextUpdate
	overflow  chan struct{}
}

func (c *sharedContextClient) bind(ctx context.Context, commandID string, work messages.WorkContext) *executionContextInbox {
	inbox := &executionContextInbox{client: c, lifetime: ctx, commandID: commandID, work: work,
		deadline: consumers.CommandDeadline(ctx), updates: make(chan agentevents.ContextUpdate, contextBufferSize), overflow: make(chan struct{})}
	c.mu.Lock()
	c.active = inbox
	c.mu.Unlock()
	return inbox
}

func (c *sharedContextClient) receive(message messages.Message) error {
	if message.AgentID != c.agentID {
		return nil
	}
	update, err := agentevents.DecodeContextUpdate(message)
	if err != nil {
		return action.ErrContextRejected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	inbox := c.active
	if inbox == nil || inbox.commandID != message.CorrelationID || inbox.work != message.Work || inbox.contextError(c.service) != nil {
		return nil
	}
	select {
	case inbox.updates <- update.Clone():
		return nil
	default:
		close(inbox.overflow)
		inbox.drain()
		return action.ErrContextOverflow
	}
}

func (r *executionContextInbox) close() {
	r.client.mu.Lock()
	defer r.client.mu.Unlock()
	if r.client.active == r {
		r.client.active = nil
	}
	r.drain()
}

func (r *executionContextInbox) drain() {
	for {
		select {
		case <-r.updates:
		default:
			return
		}
	}
}

func (r *executionContextInbox) ReceiveContext(ctx context.Context) (agentevents.ContextUpdate, error) {
	if !r.deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, r.deadline)
		defer cancel()
	}
	if err := r.contextError(ctx); err != nil {
		return agentevents.ContextUpdate{}, err
	}
	caller := ctx
	ctx, cancel := context.WithCancel(ctx)
	stopExecution := context.AfterFunc(r.lifetime, cancel)
	stopService := context.AfterFunc(r.client.service, cancel)
	defer func() { stopExecution(); stopService(); cancel() }()
	select {
	case <-ctx.Done():
		return agentevents.ContextUpdate{}, r.contextError(caller)
	case <-r.overflow:
		return agentevents.ContextUpdate{}, r.contextError(caller)
	case update := <-r.updates:
		if err := r.contextError(caller); err != nil {
			return agentevents.ContextUpdate{}, err
		}
		return update.Clone(), nil
	}
}

func (r *executionContextInbox) contextError(ctx context.Context) error {
	if err := requestCanceled(ctx, r.lifetime, r.client.service); err != nil {
		return err
	}
	if !r.deadline.IsZero() && !time.Now().Before(r.deadline) {
		return context.DeadlineExceeded
	}
	select {
	case <-r.overflow:
		return action.ErrContextOverflow
	default:
		return nil
	}
}

var _ action.ContextProvider = (*executionContextInbox)(nil)
