package consumers

import (
	"context"
	"errors"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

func (c *SecretEventConsumer) transientReceived(ctx context.Context, message messages.Message) error {
	receipt, err := controllerevents.DecodeTransientSecretReceived(message)
	if err != nil {
		return InvalidMessage(err)
	}
	record, err := c.Requests.Get(ctx, receipt.RequestID)
	if err != nil {
		return secretTransitionError(err)
	}
	request, now := record.Request, time.Now().UTC()
	if err := record.Validate(); err != nil {
		return err
	}
	if request.EffectiveType() != secrets.SecretTransient || request.AgentID != message.AgentID ||
		request.OperationID != message.Work.OperationID || request.TaskID != message.Work.TaskID ||
		message.CreatedAt.Before(request.RequestedAt) || !message.CreatedAt.Before(request.ExpiresAt) || message.CreatedAt.After(now.Add(maxAgentClockSkew)) {
		return InvalidMessage(errors.New("transient receipt does not match its request"))
	}
	if request.Status == secrets.Fulfilled && request.Fulfillment.DeliveryID == message.CorrelationID {
		c.discardTransient(request.ID)
		return secretTransitionError(c.Requests.FulfillTransient(ctx, request.ID, message.CorrelationID, now))
	}
	delivery := c.transient[request.ID]
	if request.Status != secrets.Pending || delivery == nil || delivery.message.ID != message.CorrelationID ||
		delivery.message.Work != message.Work || delivery.message.AgentID != message.AgentID || message.CreatedAt.Before(delivery.message.CreatedAt) ||
		delivery.message.CorrelationID != request.ID || !delivery.message.ExpiresAt.Equal(request.ExpiresAt) {
		return InvalidMessage(errors.New("transient receipt requires its exact held delivery"))
	}
	if delivery.receivedAt.IsZero() {
		if !delivery.executionEnded {
			if err := controllerevents.ValidateTransientSecretReceivedDelivery(message, delivery.message); err != nil {
				return InvalidMessage(err)
			}
		}
		if !now.Before(request.ExpiresAt) {
			if err := c.endTransient(ctx, request, now); err != nil {
				return err
			}
			return InvalidMessage(secrets.ErrRequestClosed)
		}
		// Receipt ends value retention even when persisting that fact is uncertain.
		// Keep only envelope metadata to confirm the same receipt on retry.
		clear(delivery.message.Payload)
		delivery.message.Payload = nil
		delivery.receivedAt = now
		if _, err := c.transientClaim(ctx, request); err != nil {
			return err
		}
	}
	if err := c.Requests.FulfillTransient(ctx, request.ID, message.CorrelationID, delivery.receivedAt); err != nil {
		return secretTransitionError(err)
	}
	c.discardTransient(request.ID)
	return nil
}

func (c *SecretEventConsumer) transientCanceled(ctx context.Context, message messages.Message) error {
	payload, err := controllerevents.DecodeSecretRequestCanceled(message)
	if err != nil {
		return InvalidMessage(err)
	}
	request, now := payload.Request, time.Now().UTC()
	if request.RequestedAt.After(now.Add(maxAgentClockSkew)) || message.CreatedAt.Before(request.RequestedAt) || message.CreatedAt.After(now.Add(maxAgentClockSkew)) {
		return InvalidMessage(errors.New("transient cancellation requires current request and cancellation timestamps"))
	}
	record, err := c.Requests.Get(ctx, request.ID)
	if errors.Is(err, secrets.ErrUnknownRequest) {
		active, validationErr := c.transientActive(ctx, request, now)
		if validationErr != nil {
			return validationErr
		}
		if !active {
			return c.transientResponse(ctx, request, agentevents.TransientSecretInactive)
		}
		if err := c.Requests.Create(ctx, request); err != nil {
			return secretTransitionError(err)
		}
		record, err = c.Requests.Get(ctx, request.ID)
	}
	if err != nil {
		return err
	}
	if !record.MatchesRequest(request) {
		return InvalidMessage(secrets.ErrRequestConflict)
	}
	if record.Request.Status != secrets.Pending {
		c.discardTransient(request.ID)
		return nil
	}
	if err := c.endTransient(ctx, request, now); err != nil {
		return err
	}
	return c.transientResponse(ctx, request, agentevents.TransientSecretCancelled)
}
