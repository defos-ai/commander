package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

type SecretDecryptor interface {
	Decrypt(secrets.Secret) ([]byte, error)
}

// SecretEventConsumer transfers encrypted values only. Its caller serializes
// Consume and Reconcile with inventory observations on the controller owner loop.
// The bus remains a trusted deployment boundary. Agents may request shared names;
// declarations are readiness metadata, not authorization.
type SecretEventConsumer struct {
	Bus       comms.Bus
	Inventory agents.AgentStore
	Secrets   secrets.SecretStore
	Requests  secrets.RequestStore
	Decryptor SecretDecryptor
}

func (c SecretEventConsumer) Consume(ctx context.Context, message messages.Message) error {
	switch message.Type {
	case controllerevents.SecretSyncRequested:
		return c.request(ctx, message)
	case controllerevents.SecretInstalled:
		return c.installed(ctx, message)
	default:
		return InvalidMessage(errors.New("unsupported secret event"))
	}
}

func (c SecretEventConsumer) request(ctx context.Context, message messages.Message) error {
	payload, err := controllerevents.DecodeSecretSyncRequested(message)
	if err != nil {
		return InvalidMessage(err)
	}
	request, now := payload.Request, time.Now().UTC()
	if request.RequestedAt.After(now.Add(maxAgentClockSkew)) {
		return InvalidMessage(errors.New("secret synchronization requires an agent request with a current timestamp"))
	}
	allowed, err := c.allowed(ctx, request, now)
	if err != nil {
		return err
	}
	if !allowed {
		return InvalidMessage(errors.New("secret request must match a live agent's registered type"))
	}
	if err := c.Requests.Create(ctx, request); err != nil {
		return secretTransitionError(err)
	}
	record, err := c.Requests.Get(ctx, request.ID)
	if err != nil {
		return err
	}
	return c.deliver(ctx, record, time.Now().UTC())
}

// Reconcile retries durable pending deliveries after restart or a missed agent
// event. Missing values remain pending so supplying them later needs no new
// request. A delivery reserved for an old version is never replaced in place.
func (c SecretEventConsumer) Reconcile(ctx context.Context) error {
	records, err := c.Requests.ListPending(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := c.deliver(ctx, record, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (c SecretEventConsumer) deliver(ctx context.Context, record secrets.RequestRecord, now time.Time) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("invalid saved secret request: %w", err)
	}
	request := record.Request
	if request.Status != secrets.Pending {
		return nil
	}
	if !now.Before(request.ExpiresAt) {
		return c.Requests.Expire(ctx, request.ID, now)
	}
	if now.Before(request.RequestedAt) {
		return nil
	}
	allowed, err := c.allowed(ctx, request, now)
	if err != nil || !allowed {
		return err
	}
	secret, available, err := c.current(ctx, request.Key, now)
	if err != nil {
		return err
	}
	if record.Delivery != nil {
		metadata := record.Delivery.Secret.Metadata
		if !available || metadata.ID != secret.Metadata.ID || metadata.Version != secret.Metadata.Version || !now.Before(record.Delivery.ExpiresAt) {
			return c.Requests.Cancel(ctx, request.ID, now)
		}
	} else {
		if !available {
			return nil
		}
		message, err := agentevents.NewSecretDelivery(request, secret)
		if err != nil {
			now = time.Now().UTC()
			if !now.Before(request.ExpiresAt) {
				return c.Requests.Expire(ctx, request.ID, now)
			}
			if secrets.AvailabilityAt(&secret.Metadata, now) != secrets.Available {
				return nil
			}
			return err
		}
		delivery := secrets.Delivery{
			ID: message.ID, RequestID: request.ID, AgentID: request.AgentID, Secret: secret,
			CreatedAt: message.CreatedAt, ExpiresAt: message.ExpiresAt,
		}
		// An uncertain save stops processing. Recovery rereads the record before
		// deciding whether the reserved delivery can be sent.
		if err := c.Requests.SaveDelivery(ctx, delivery); err != nil {
			return err
		}
		record.Delivery = &delivery
	}
	if c.Decryptor != nil {
		value, err := c.Decryptor.Decrypt(record.Delivery.Secret.Clone())
		clear(value)
		if err != nil {
			return errors.New("cannot authenticate secret for delivery")
		}
	}
	// Storage and decryption can take time. Recheck validity and the latest value
	// before sending; this is not a cross-store transaction with rotation.
	now = time.Now().UTC()
	if !now.Before(request.ExpiresAt) {
		return c.Requests.Expire(ctx, request.ID, now)
	}
	secret, available, err = c.current(ctx, request.Key, now)
	if err != nil {
		return err
	}
	metadata := record.Delivery.Secret.Metadata
	if !available || metadata.ID != secret.Metadata.ID || metadata.Version != secret.Metadata.Version || !now.Before(record.Delivery.ExpiresAt) {
		return c.Requests.Cancel(ctx, request.ID, now)
	}
	message, err := deliveryMessage(*record.Delivery)
	if err != nil {
		return err
	}
	return (agentevents.Client{Bus: c.Bus}).Publish(ctx, message)
}

func (c SecretEventConsumer) installed(ctx context.Context, message messages.Message) error {
	payload, err := controllerevents.DecodeSecretInstalled(message)
	if err != nil {
		return InvalidMessage(err)
	}
	record, err := c.Requests.Get(ctx, payload.RequestID)
	if err != nil {
		return secretTransitionError(err)
	}
	now := time.Now().UTC()
	installation := secrets.Installation{
		ID: message.ID, RequestID: payload.RequestID, DeliveryID: message.CorrelationID,
		AgentID: message.AgentID, SecretID: payload.SecretID, SecretVersion: payload.SecretVersion,
		InstalledAt: message.CreatedAt,
	}
	// Check the complete evidence before any mutation or terminal replay. Exact
	// old acknowledgements remain historical facts after rotation or shutdown.
	if _, err := record.Acknowledge(installation, now); err != nil {
		return InvalidMessage(err)
	}
	if record.Request.Status == secrets.Fulfilled {
		return secretTransitionError(c.Requests.Acknowledge(ctx, installation, now))
	}
	allowed, err := c.allowed(ctx, record.Request, now)
	if err != nil {
		return err
	}
	if !allowed {
		return InvalidMessage(errors.New("secret installation must match a live agent's registered type"))
	}
	secret, available, err := c.current(ctx, record.Request.Key, now)
	if err != nil {
		return err
	}
	if !available || secret.Metadata.ID != payload.SecretID || secret.Metadata.Version != payload.SecretVersion {
		if err := c.Requests.Cancel(ctx, record.Request.ID, now); err != nil {
			return err
		}
		return InvalidMessage(errors.New("secret installation refers to a superseded or unavailable value"))
	}
	// The current-version read and this write are not a cross-store transaction.
	// Fulfillment is installation history, never ongoing agent readiness.
	return secretTransitionError(c.Requests.Acknowledge(ctx, installation, now))
}

func (c SecretEventConsumer) allowed(ctx context.Context, request secrets.Request, now time.Time) (bool, error) {
	record, err := c.Inventory.Get(ctx, request.AgentID)
	if errors.Is(err, agents.ErrUnknownAgent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	record = record.Snapshot(now)
	if record.ID != request.AgentID || record.Stale || record.Status == agents.AgentStopped || string(record.Type) != request.AgentType {
		return false, nil
	}
	return true, nil
}

func (c SecretEventConsumer) current(ctx context.Context, key secrets.Key, now time.Time) (secrets.Secret, bool, error) {
	secret, err := c.Secrets.Get(ctx, key)
	if errors.Is(err, secrets.ErrUnknownSecret) {
		return secrets.Secret{}, false, nil
	}
	if err != nil {
		return secrets.Secret{}, false, err
	}
	if err := secret.Validate(); err != nil || secret.Metadata.Key != key {
		return secrets.Secret{}, false, errors.New("invalid saved encrypted secret")
	}
	return secret, !secret.Metadata.UpdatedAt.After(now) && secrets.AvailabilityAt(&secret.Metadata, now) == secrets.Available, nil
}

func deliveryMessage(delivery secrets.Delivery) (messages.Message, error) {
	payload, err := json.Marshal(agentevents.SecretDelivery{Secret: delivery.Secret})
	if err != nil {
		return messages.Message{}, err
	}
	return messages.Message{
		ID: delivery.ID, Type: agentevents.SecretDelivered, AgentID: delivery.AgentID,
		CorrelationID: delivery.RequestID, CreatedAt: delivery.CreatedAt, ExpiresAt: delivery.ExpiresAt,
		Payload: payload,
	}, nil
}

func secretTransitionError(err error) error {
	if errors.Is(err, secrets.ErrWriteUncertain) {
		return err
	}
	if errors.Is(err, secrets.ErrUnknownRequest) || errors.Is(err, secrets.ErrRequestConflict) || errors.Is(err, secrets.ErrRequestClosed) || errors.Is(err, secrets.ErrRequestExpired) {
		return InvalidMessage(err)
	}
	return err
}
