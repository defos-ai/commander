package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
)

const checkpointRetryInterval = 500 * time.Millisecond

type checkpointClientKey struct{}

// A serving instance shares its existing event inbox across checkpoint calls.
// Requests are registered before publication and removed when the caller exits.
type checkpointClient struct {
	agentID string
	service context.Context
	events  controllerevents.Client
	mu      sync.Mutex
	pending map[string]checkpointWait
}

type checkpointWait struct {
	message messages.Message
	request controllerevents.CheckpointRequest
	reply   chan agentevents.CheckpointResponse
}

type executionCheckpoints struct {
	client    *checkpointClient
	lifetime  context.Context
	commandID string
	work      messages.WorkContext
}

func (r *executionCheckpoints) LoadCheckpoint(ctx context.Context, name string) (json.RawMessage, bool, error) {
	reply, err := r.request(ctx, name, controllerevents.CheckpointLoad, nil)
	if err != nil {
		return nil, false, err
	}
	if reply.Error == agentevents.CheckpointNotFound {
		return nil, false, nil
	}
	if err := checkpointError(reply.Error); err != nil {
		return nil, false, err
	}
	return bytes.Clone(reply.Data), true, nil
}

func (r *executionCheckpoints) SaveCheckpoint(ctx context.Context, name string, data json.RawMessage) error {
	reply, err := r.request(ctx, name, controllerevents.CheckpointSave, data)
	if err != nil {
		return err
	}
	return checkpointError(reply.Error)
}

func (r *executionCheckpoints) request(ctx context.Context, name, operation string, data json.RawMessage) (agentevents.CheckpointResponse, error) {
	// Detached contexts cannot outlive the action or serving instance that
	// granted this capability.
	if err := r.contextError(ctx); err != nil {
		return agentevents.CheckpointResponse{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stopExecution := context.AfterFunc(r.lifetime, cancel)
	stopService := context.AfterFunc(r.client.service, cancel)
	defer func() { stopExecution(); stopService(); cancel() }()
	request := controllerevents.CheckpointRequest{
		CommandID: r.commandID, Name: name, Operation: operation, Data: bytes.Clone(data),
	}
	message, err := controllerevents.NewCheckpointRequest(r.client.agentID, r.work, request)
	if err != nil {
		return agentevents.CheckpointResponse{}, err
	}
	wait := checkpointWait{message: message, request: request, reply: make(chan agentevents.CheckpointResponse, 1)}
	r.client.mu.Lock()
	r.client.pending[message.ID] = wait
	r.client.mu.Unlock()
	defer func() {
		r.client.mu.Lock()
		delete(r.client.pending, message.ID)
		r.client.mu.Unlock()
	}()
	ticker := time.NewTicker(checkpointRetryInterval)
	defer ticker.Stop()
	for {
		if err := r.contextError(ctx); err != nil {
			return agentevents.CheckpointResponse{}, err
		}
		publication, done := context.WithTimeout(ctx, 5*time.Second)
		// A publication error is not proof of non-delivery. Only these immutable
		// checkpoint requests may retry; never rerun the action here.
		_ = r.client.events.Publish(publication, message)
		done()
		select {
		case <-ctx.Done():
			return agentevents.CheckpointResponse{}, ctx.Err()
		case reply := <-wait.reply:
			if err := r.contextError(ctx); err != nil {
				return agentevents.CheckpointResponse{}, err
			}
			return reply, nil
		case <-ticker.C:
		}
	}
}

func (r *executionCheckpoints) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// AfterFunc wakes detached waits asynchronously; check the owning lifetimes
	// directly too before publishing or accepting a response.
	if err := r.lifetime.Err(); err != nil {
		return err
	}
	return r.client.service.Err()
}

func (c *checkpointClient) receive(message messages.Message) {
	if message.AgentID != c.agentID {
		return
	}
	response, err := agentevents.DecodeCheckpointResponse(message)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	wait, exists := c.pending[message.CorrelationID]
	if !exists || response.CommandID != wait.request.CommandID || response.Name != wait.request.Name ||
		message.Work != wait.message.Work {
		return
	}
	if wait.request.Operation == controllerevents.CheckpointSave {
		if response.Error == agentevents.CheckpointNotFound {
			return
		}
		if response.Error == "" && !sameCheckpointData(response.Data, wait.request.Data) {
			return
		}
	}
	response.Data = bytes.Clone(response.Data)
	select {
	case wait.reply <- response:
	default:
	}
}

func checkpointError(code string) error {
	switch code {
	case "":
		return nil
	case agentevents.CheckpointConflict:
		return executions.ErrCheckpointConflict
	case agentevents.CheckpointCompleted:
		return executions.ErrExecutionCompleted
	case agentevents.CheckpointUnknownExecution:
		return executions.ErrUnknownExecution
	default:
		return action.ErrCheckpointRejected
	}
}

func sameCheckpointData(a, b json.RawMessage) bool {
	var left, right any
	decode := func(data json.RawMessage, value *any) error {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		return decoder.Decode(value)
	}
	return decode(a, &left) == nil && decode(b, &right) == nil && reflect.DeepEqual(left, right)
}

var _ action.CheckpointProvider = (*executionCheckpoints)(nil)
