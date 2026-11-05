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

const sharedDataBufferSize = 32

type sharedDataClientKey struct{}

type sharedDataClient struct {
	agentID string
	service context.Context
	mu      sync.Mutex
	active  *executionDataInbox
}

type executionDataInbox struct {
	client      *sharedDataClient
	lifetime    context.Context
	commandID   string
	operationID string
	taskID      string
	deadline    time.Time
	updates     chan agentevents.SharedData
	overflow    chan struct{}
}

func (c *sharedDataClient) bind(ctx context.Context, commandID, operationID, taskID string) *executionDataInbox {
	inbox := &executionDataInbox{client: c, lifetime: ctx, commandID: commandID, operationID: operationID, taskID: taskID,
		deadline: consumers.CommandDeadline(ctx), updates: make(chan agentevents.SharedData, sharedDataBufferSize), overflow: make(chan struct{})}
	c.mu.Lock()
	c.active = inbox
	c.mu.Unlock()
	return inbox
}

func (c *sharedDataClient) receive(message messages.Message) error {
	if message.AgentID != c.agentID {
		return nil
	}
	update, err := agentevents.DecodeSharedData(message)
	if err != nil {
		return action.ErrSharedDataRejected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	inbox := c.active
	if inbox == nil || inbox.commandID != message.CorrelationID || inbox.operationID != message.OperationID || inbox.taskID != message.TaskID || inbox.contextError(c.service) != nil {
		return nil
	}
	select {
	case inbox.updates <- update.Clone():
		return nil
	default:
		close(inbox.overflow)
		inbox.drain()
		return action.ErrSharedDataOverflow
	}
}

func (r *executionDataInbox) close() {
	r.client.mu.Lock()
	defer r.client.mu.Unlock()
	if r.client.active == r {
		r.client.active = nil
	}
	r.drain()
}

func (r *executionDataInbox) drain() {
	for {
		select {
		case <-r.updates:
		default:
			return
		}
	}
}

func (r *executionDataInbox) ReceiveSharedData(ctx context.Context) (agentevents.SharedData, error) {
	if !r.deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, r.deadline)
		defer cancel()
	}
	if err := r.contextError(ctx); err != nil {
		return agentevents.SharedData{}, err
	}
	caller := ctx
	ctx, cancel := context.WithCancel(ctx)
	stopExecution := context.AfterFunc(r.lifetime, cancel)
	stopService := context.AfterFunc(r.client.service, cancel)
	defer func() { stopExecution(); stopService(); cancel() }()
	select {
	case <-ctx.Done():
		return agentevents.SharedData{}, r.contextError(caller)
	case <-r.overflow:
		return agentevents.SharedData{}, r.contextError(caller)
	case update := <-r.updates:
		if err := r.contextError(caller); err != nil {
			return agentevents.SharedData{}, err
		}
		return update.Clone(), nil
	}
}

func (r *executionDataInbox) contextError(ctx context.Context) error {
	if err := requestCanceled(ctx, r.lifetime, r.client.service); err != nil {
		return err
	}
	if !r.deadline.IsZero() && !time.Now().Before(r.deadline) {
		return context.DeadlineExceeded
	}
	select {
	case <-r.overflow:
		return action.ErrSharedDataOverflow
	default:
		return nil
	}
}

var _ action.SharedDataProvider = (*executionDataInbox)(nil)
