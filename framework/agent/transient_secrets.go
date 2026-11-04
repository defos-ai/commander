package agent

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/consumers"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

type transientSecretWait struct {
	request     secrets.Request
	message     messages.Message
	receipt     messages.Message
	deliveredAt time.Time
	value       []byte
	err         error
	caller      context.Context
	execution   context.Context
}

type transientSecretReceipt struct {
	request     secrets.Request
	message     messages.Message
	deliveredAt time.Time
}

func (r *secretRuntime) requestTransient(ctx context.Context, waits *workSecretWaits, name string) ([]byte, error) {
	if r == nil || waits == nil {
		return nil, action.ErrSecretUnavailable
	}
	execution, ok := action.ExecutionFromContext(waits.context)
	if !ok || execution.CommandID != waits.commandID || execution.Work.OperationID == "" {
		return nil, action.ErrSecretUnavailable
	}
	now := time.Now().UTC()
	deadline := now.Add(r.config.RequestTTL)
	for _, limit := range []time.Time{consumers.CommandDeadline(waits.context), contextDeadline(ctx), contextDeadline(waits.context)} {
		if !limit.IsZero() && limit.Before(deadline) {
			deadline = limit
		}
	}
	if !now.Before(deadline) {
		return nil, context.DeadlineExceeded
	}
	request := secrets.Request{
		ID: id.New(), Type: secrets.SecretTransient, Key: secrets.Key{Name: name},
		AgentID: r.agentID, AgentType: r.typeName, OperationID: execution.Work.OperationID,
		TaskID: execution.Work.TaskID, CommandID: execution.CommandID,
		Status: secrets.Pending, RequestedAt: now, ExpiresAt: deadline,
	}
	message, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		return nil, action.ErrSecretUnavailable
	}
	r.mu.Lock()
	service := r.service
	if service == nil || requestCanceled(ctx, waits.context, service) != nil {
		r.mu.Unlock()
		if err := requestCanceled(ctx, waits.context, service); err != nil {
			return nil, err
		}
		return nil, action.ErrSecretUnavailable
	}
	wait := &transientSecretWait{request: request, message: message, caller: ctx, execution: waits.context}
	r.transient[request.ID] = wait
	r.mu.Unlock()
	defer r.finishTransient(wait)
	done, err := waits.begin(ctx, name)
	if err != nil {
		return nil, err
	}
	defer done()
	r.notify()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		r.mu.Lock()
		if err := requestCanceled(ctx, waits.context, service); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			r.mu.Unlock()
			return nil, context.DeadlineExceeded
		}
		if wait.err != nil {
			err := wait.err
			r.mu.Unlock()
			return nil, err
		}
		if wait.receipt.ID != "" {
			value := bytes.Clone(wait.value)
			r.mu.Unlock()
			return value, nil
		}
		changed := r.changed
		r.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-waits.context.Done():
		case <-service.Done():
		case <-timer.C:
		case <-changed:
		}
	}
}

func contextDeadline(ctx context.Context) time.Time {
	deadline, _ := ctx.Deadline()
	return deadline
}

func (r *secretRuntime) finishTransient(wait *transientSecretWait) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(wait.value)
	wait.value = nil
	delete(r.transient, wait.request.ID)
	if r.service == nil || r.service.Err() != nil {
		return
	}
	message := wait.receipt
	if message.ID == "" {
		var err error
		message, err = controllerevents.NewSecretRequestCanceled(wait.request)
		if err != nil {
			return
		}
	}
	// Only metadata remains while receipts retry; no value is cached for a later call.
	r.receipts[wait.request.ID] = transientSecretReceipt{request: wait.request, message: message, deliveredAt: wait.deliveredAt}
	r.notify()
}

// pendingTransient runs under the secret runtime mutex and never renews requests.
func (r *secretRuntime) pendingTransient(now time.Time) []messages.Message {
	var outgoing []messages.Message
	for _, wait := range r.transient {
		if !now.Before(wait.request.ExpiresAt) || wait.err != nil {
			continue
		}
		if wait.receipt.ID == "" && requestCanceled(wait.caller, wait.execution, r.service) != nil {
			continue
		}
		message := wait.message
		if wait.receipt.ID != "" {
			message = wait.receipt
		}
		outgoing = append(outgoing, message)
	}
	for requestID, receipt := range r.receipts {
		if !now.Before(receipt.request.ExpiresAt) {
			delete(r.receipts, requestID)
			continue
		}
		outgoing = append(outgoing, receipt.message)
	}
	return outgoing
}

func (r *secretRuntime) receiveTransient(message messages.Message, now time.Time) error {
	response, err := agentevents.DecodeTransientSecretResponse(message)
	if err != nil {
		return errors.New("invalid transient secret delivery")
	}
	defer clear(response.Value)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.service == nil || r.service.Err() != nil {
		return action.ErrSecretUnavailable
	}
	wait := r.transient[message.CorrelationID]
	if wait == nil {
		receipt, ok := r.receipts[message.CorrelationID]
		if !ok || receipt.message.Type != controllerevents.SecretTransientReceived || response.Error != "" ||
			receipt.message.CorrelationID != message.ID || !receipt.deliveredAt.Equal(message.CreatedAt) ||
			agentevents.ValidateTransientSecretResponseRequest(message, receipt.request) != nil || !now.Before(receipt.request.ExpiresAt) {
			return action.ErrSecretUnavailable
		}
		r.notify()
		return nil
	}
	if agentevents.ValidateTransientSecretResponseRequest(message, wait.request) != nil || now.Before(message.CreatedAt) {
		return errors.New("transient secret delivery does not match its request")
	}
	if !now.Before(wait.request.ExpiresAt) {
		return context.DeadlineExceeded
	}
	if wait.receipt.ID != "" {
		if response.Error != "" || wait.receipt.CorrelationID != message.ID || !wait.deliveredAt.Equal(message.CreatedAt) || !bytes.Equal(wait.value, response.Value) {
			return errors.New("conflicting transient secret delivery")
		}
		r.notify()
		return nil
	}
	if wait.err != nil {
		return action.ErrSecretUnavailable
	}
	if err := requestCanceled(wait.caller, wait.execution, r.service); err != nil {
		return err
	}
	if response.Error != "" {
		wait.err = action.ErrSecretUnavailable
		if response.Error == agentevents.TransientSecretExpired {
			wait.err = context.DeadlineExceeded
		}
		r.broadcast()
		return nil
	}
	receipt, err := controllerevents.NewTransientSecretReceived(message)
	if err != nil {
		return errors.New("transient secret delivery expired before receipt")
	}
	wait.value, wait.receipt, wait.deliveredAt = bytes.Clone(response.Value), receipt, message.CreatedAt
	r.broadcast()
	r.notify()
	return nil
}
