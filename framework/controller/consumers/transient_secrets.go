package consumers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/executions"
	"github.com/defos-ai/commander/framework/domain/operations"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

type transientDelivery struct {
	message        messages.Message
	receivedAt     time.Time
	executionEnded bool
}

func (*SecretEventConsumer) String() string   { return "controller secrets (values redacted)" }
func (*SecretEventConsumer) GoString() string { return "controller secrets (values redacted)" }

// InitializeTransient ends waits whose values cannot survive this controller.
// It runs once before intake; periodic reconciliation must not cancel fresh waits.
func (c *SecretEventConsumer) InitializeTransient(ctx context.Context) error {
	c.ClearTransient()
	records, err := c.Requests.ListPending(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Request.EffectiveType() != secrets.SecretTransient {
			continue
		}
		if err := c.endTransient(ctx, record.Request, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (c *SecretEventConsumer) ClearTransient() {
	for requestID := range c.transient {
		c.discardTransient(requestID)
	}
}

func (c *SecretEventConsumer) discardTransient(requestID string) {
	if delivery := c.transient[requestID]; delivery != nil {
		clear(delivery.message.Payload)
		delete(c.transient, requestID)
	}
}

func (c *SecretEventConsumer) transientRequested(ctx context.Context, request secrets.Request) error {
	record, err := c.Requests.Get(ctx, request.ID)
	if err == nil {
		if !record.MatchesRequest(request) {
			return c.transientResponse(ctx, request, agentevents.TransientSecretConflict)
		}
		return c.deliverTransient(ctx, record, time.Now().UTC())
	}
	if !errors.Is(err, secrets.ErrUnknownRequest) {
		return err
	}
	active, err := c.transientActive(ctx, request, time.Now().UTC())
	if err != nil {
		return err
	}
	if !active {
		return c.transientResponse(ctx, request, agentevents.TransientSecretInactive)
	}
	if err := c.Requests.Create(ctx, request); err != nil {
		return secretTransitionError(err)
	}
	record, err = c.Requests.Get(ctx, request.ID)
	if err != nil {
		return err
	}
	return c.deliverTransient(ctx, record, time.Now().UTC())
}

// ProvideTransient retains a copy only until receipt, cancellation or expiry.
// The caller must run it under the same owner as Consume and Reconcile.
func (c *SecretEventConsumer) ProvideTransient(ctx context.Context, requestID string, value []byte) error {
	if value == nil || len(value) > secrets.MaxTransientSecretBytes {
		return errors.New("transient secret requires a value within the size limit")
	}
	record, err := c.Requests.Get(ctx, requestID)
	if err != nil {
		return err
	}
	request := record.Request
	if err := record.Validate(); err != nil {
		return err
	}
	if request.EffectiveType() != secrets.SecretTransient {
		return secrets.ErrRequestConflict
	}
	if request.Status != secrets.Pending {
		c.discardTransient(request.ID)
		return secrets.ErrRequestClosed
	}
	now := time.Now().UTC()
	if !now.Before(request.ExpiresAt) {
		c.discardTransient(request.ID)
		if err := c.Requests.Expire(ctx, request.ID, now); err != nil {
			return err
		}
		return secrets.ErrRequestExpired
	}
	if awaiting, err := c.awaitTransientReceipt(ctx, request); err != nil {
		return err
	} else if awaiting {
		return secrets.ErrRequestClosed
	}
	active, err := c.transientActive(ctx, request, now)
	if err != nil {
		return err
	}
	allowed, err := c.allowed(ctx, request, now)
	if err != nil {
		return err
	}
	if !active || !allowed {
		if err := c.endTransient(ctx, request, now); err != nil {
			return err
		}
		return secrets.ErrRequestClosed
	}
	if delivery := c.transient[requestID]; delivery != nil {
		if !delivery.receivedAt.IsZero() {
			return secrets.ErrRequestClosed
		}
		payload, err := agentevents.DecodeTransientSecretResponse(delivery.message)
		if err != nil {
			return err
		}
		matches := bytes.Equal(payload.Value, value)
		clear(payload.Value)
		if !matches {
			return secrets.ErrRequestConflict
		}
	} else {
		message, err := agentevents.NewTransientSecretResponse(request, value, "")
		if err != nil {
			return err
		}
		if c.transient == nil {
			c.transient = make(map[string]*transientDelivery)
		}
		c.transient[requestID] = &transientDelivery{message: message}
	}
	return c.deliverTransient(ctx, record, time.Now().UTC())
}

func (c *SecretEventConsumer) deliverTransient(ctx context.Context, record secrets.RequestRecord, now time.Time) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("invalid transient secret metadata: %w", err)
	}
	request := record.Request
	if request.Status != secrets.Pending {
		c.discardTransient(request.ID)
		code := agentevents.TransientSecretInactive
		if request.Status == secrets.RequestExpired {
			code = agentevents.TransientSecretExpired
		} else if request.Status == secrets.Cancelled {
			code = agentevents.TransientSecretCancelled
		}
		return c.transientResponse(ctx, request, code)
	}
	delivery := c.transient[request.ID]
	if delivery != nil && !delivery.receivedAt.IsZero() {
		if err := c.Requests.FulfillTransient(ctx, request.ID, delivery.message.ID, delivery.receivedAt); err != nil {
			return err
		}
		c.discardTransient(request.ID)
		return nil
	}
	if !now.Before(request.ExpiresAt) {
		if err := c.endTransient(ctx, request, now); err != nil {
			return err
		}
		return c.transientResponse(ctx, request, agentevents.TransientSecretExpired)
	}
	if now.Before(request.RequestedAt) {
		return nil
	}
	if awaiting, err := c.awaitTransientReceipt(ctx, request); awaiting || err != nil {
		return err
	}
	active, err := c.transientActive(ctx, request, now)
	if err != nil {
		return err
	}
	allowed, err := c.allowed(ctx, request, now)
	if err != nil {
		return err
	}
	if !active || !allowed {
		if err := c.endTransient(ctx, request, now); err != nil {
			return err
		}
		return c.transientResponse(ctx, request, agentevents.TransientSecretInactive)
	}
	if delivery == nil {
		return nil
	}
	return (agentevents.Client{Bus: c.Bus}).Publish(ctx, delivery.message)
}

func (c *SecretEventConsumer) awaitTransientReceipt(ctx context.Context, request secrets.Request) (bool, error) {
	delivery := c.transient[request.ID]
	if delivery == nil || !delivery.receivedAt.IsZero() {
		return false, nil
	}
	if delivery.executionEnded {
		return true, nil
	}
	execution, err := c.transientClaim(ctx, request)
	if errors.Is(err, executions.ErrUnknownExecution) {
		return false, nil
	}
	if err != nil || execution.Result == nil {
		return false, err
	}
	// Results and receipts can arrive independently. End value retention while
	// keeping the exact envelope for a receipt within the original lifetime.
	clear(delivery.message.Payload)
	delivery.message.Payload = nil
	delivery.executionEnded = true
	return true, nil
}

func (c *SecretEventConsumer) endTransient(ctx context.Context, request secrets.Request, now time.Time) error {
	c.discardTransient(request.ID)
	if !now.Before(request.ExpiresAt) {
		return c.Requests.Expire(ctx, request.ID, now)
	}
	if now.Before(request.RequestedAt) {
		now = request.RequestedAt
	}
	return c.Requests.Cancel(ctx, request.ID, now)
}

func (c *SecretEventConsumer) transientResponse(ctx context.Context, request secrets.Request, code string) error {
	if time.Now().Before(request.RequestedAt) {
		return nil
	}
	request.Status, request.ResolvedAt, request.Fulfillment = secrets.Pending, time.Time{}, nil
	message, err := agentevents.NewTransientSecretResponse(request, nil, code)
	if err != nil {
		return err
	}
	return (agentevents.Client{Bus: c.Bus}).Publish(ctx, message)
}

func (c *SecretEventConsumer) transientActive(ctx context.Context, request secrets.Request, now time.Time) (bool, error) {
	if c.Executions == nil || c.Operations == nil {
		return false, nil
	}
	record, err := c.transientClaim(ctx, request)
	if errors.Is(err, executions.ErrUnknownExecution) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if record.Result != nil {
		return false, nil
	}
	op, err := c.Operations.Get(ctx, request.OperationID)
	if errors.Is(err, operations.ErrUnknownOperation) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	claim := messages.Message{AgentID: request.AgentID, CorrelationID: request.CommandID,
		OperationID: request.OperationID, TaskID: request.TaskID}
	if !questionExecutionActive(op, claim, now) {
		return false, nil
	}
	for _, task := range op.Tasks {
		if task.ID == request.TaskID && request.ExpiresAt.After(*task.DeadlineAt) {
			return false, InvalidMessage(errors.New("transient request exceeds its execution deadline"))
		}
	}
	return true, nil
}

func (c *SecretEventConsumer) transientClaim(ctx context.Context, request secrets.Request) (executions.Execution, error) {
	if c.Executions == nil {
		return executions.Execution{}, executions.ErrUnknownExecution
	}
	record, err := c.Executions.Get(ctx, request.AgentID, request.CommandID)
	if err != nil {
		return executions.Execution{}, err
	}
	claim := messages.Message{AgentID: request.AgentID, CorrelationID: request.CommandID,
		OperationID: request.OperationID, TaskID: request.TaskID}
	if err := checkpointOwner(record, claim); err != nil {
		return executions.Execution{}, err
	}
	return record, nil
}
