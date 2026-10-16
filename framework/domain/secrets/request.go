package secrets

import (
	"errors"
	"time"

	"github.com/defos-ai/commander/framework/id"
)

type RequestStatus string

const (
	Pending        RequestStatus = "pending"
	Fulfilled      RequestStatus = "fulfilled"
	RequestExpired RequestStatus = "expired"
	Cancelled      RequestStatus = "cancelled"
)

var (
	ErrRequestClosed  = errors.New("secret request is already settled")
	ErrRequestExpired = errors.New("secret request has expired")
)

// Fulfillment records the version acknowledged by the requesting agent for a
// particular delivery. It is historical evidence, not proof of current readiness.
type Fulfillment struct {
	SecretID      string `json:"secret_id"`
	SecretVersion uint64 `json:"secret_version"`
	DeliveryID    string `json:"delivery_id"`
}

// Request tracks an agent's need for a secret independently of secret storage.
// OperationID optionally identifies work waiting on it; it does not own the secret.
// AgentID is the recipient, which may differ from the type in Key's scope (for
// example a builder preparing configuration for a generated agent).
type Request struct {
	ID          string        `json:"id"`
	Key         Key           `json:"key"`
	AgentID     string        `json:"agent_id"`
	OperationID string        `json:"operation_id,omitempty"`
	Status      RequestStatus `json:"status"`
	RequestedAt time.Time     `json:"requested_at"`
	ExpiresAt   time.Time     `json:"expires_at"`
	ResolvedAt  time.Time     `json:"resolved_at,omitzero"`
	Fulfillment *Fulfillment  `json:"fulfillment,omitempty"`
}

func (request Request) Validate() error {
	if !id.Valid(request.ID) || !id.ValidAgent(request.AgentID) {
		return errors.New("secret request requires a canonical UUID and recipient agent ID")
	}
	if request.OperationID != "" && !id.Valid(request.OperationID) {
		return errors.New("secret request operation ID must be a canonical UUID")
	}
	if err := request.Key.Validate(); err != nil {
		return err
	}
	if !validTime(request.RequestedAt) || !validTime(request.ExpiresAt) || !request.ExpiresAt.After(request.RequestedAt) {
		return errors.New("secret request requires valid timestamps and a deadline after creation")
	}
	if request.Status == Pending {
		if !request.ResolvedAt.IsZero() || request.Fulfillment != nil {
			return errors.New("pending secret request must not have a resolution")
		}
		return nil
	}
	if !validTime(request.ResolvedAt) || request.ResolvedAt.Before(request.RequestedAt) {
		return errors.New("settled secret request requires a resolution time at or after creation")
	}
	switch request.Status {
	case Fulfilled, Cancelled:
		if !request.ResolvedAt.Before(request.ExpiresAt) {
			return ErrRequestExpired
		}
	case RequestExpired:
		if request.ResolvedAt.Before(request.ExpiresAt) {
			return errors.New("secret request cannot expire before its deadline")
		}
	default:
		return errors.New("invalid secret request status")
	}
	if request.Status == Fulfilled {
		value := request.Fulfillment
		if value == nil || !id.Valid(value.SecretID) || value.SecretVersion == 0 || !id.Valid(value.DeliveryID) {
			return errors.New("fulfilled secret request requires secret identity, version and delivery ID")
		}
	} else if request.Fulfillment != nil {
		return errors.New("unfulfilled secret request must not have fulfillment data")
	}
	return nil
}

// Fulfill applies an authenticated delivery acknowledgement. The caller must
// verify that the delivery belongs to this request/recipient and still matches
// the current stored secret. Merely supplying a secret must not call Fulfill.
// Replays of the same acknowledgement preserve the original settlement time.
func (request Request) Fulfill(secret Metadata, deliveryID string, now time.Time) (Request, error) {
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	if err := secret.Validate(); err != nil {
		return Request{}, err
	}
	if secret.Key != request.Key || !id.Valid(deliveryID) {
		return Request{}, errors.New("secret fulfillment must match the requested key and identify its delivery")
	}
	fulfillment := Fulfillment{SecretID: secret.ID, SecretVersion: secret.Version, DeliveryID: deliveryID}
	if request.Status == Fulfilled && *request.Fulfillment == fulfillment {
		return request.Clone(), nil
	}
	if err := request.pendingAt(now); err != nil {
		return Request{}, err
	}
	if secret.UpdatedAt.After(now) || AvailabilityAt(&secret, now) != Available {
		return Request{}, errors.New("secret must be available at fulfillment time")
	}
	request.Status, request.ResolvedAt, request.Fulfillment = Fulfilled, now, &fulfillment
	return request, nil
}

func (request Request) Cancel(now time.Time) (Request, error) {
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	if request.Status == Cancelled {
		return request.Clone(), nil
	}
	if err := request.pendingAt(now); err != nil {
		return Request{}, err
	}
	request.Status, request.ResolvedAt = Cancelled, now
	return request, nil
}

func (request Request) Expire(now time.Time) (Request, error) {
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	if request.Status == RequestExpired {
		return request.Clone(), nil
	}
	if request.Status != Pending {
		return Request{}, ErrRequestClosed
	}
	if !validTime(now) || now.Before(request.ExpiresAt) {
		return Request{}, errors.New("secret request deadline has not elapsed")
	}
	request.Status, request.ResolvedAt = RequestExpired, now
	return request, nil
}

func (request Request) Clone() Request {
	if request.Fulfillment != nil {
		value := *request.Fulfillment
		request.Fulfillment = &value
	}
	return request
}

func (request Request) pendingAt(now time.Time) error {
	if request.Status != Pending {
		return ErrRequestClosed
	}
	if !validTime(now) || now.Before(request.RequestedAt) {
		return errors.New("secret request transition requires a valid time at or after creation")
	}
	if !now.Before(request.ExpiresAt) {
		return ErrRequestExpired
	}
	return nil
}
