package secrets

import (
	"errors"
	"time"
)

// RequestRecord is one atomic persistence unit. One immutable delivery belongs
// to one request; cancel it and create a new request to deliver a newer version.
// Keeping terminal records prevents late retries from reopening settled work.
type RequestRecord struct {
	Request      Request       `json:"request"`
	Delivery     *Delivery     `json:"delivery,omitempty"`
	Installation *Installation `json:"installation,omitempty"`
}

func NewRequestRecord(request Request) (RequestRecord, error) {
	if err := request.Validate(); err != nil {
		return RequestRecord{}, err
	}
	if request.Status != Pending {
		return RequestRecord{}, errors.New("new secret request must be pending")
	}
	return RequestRecord{Request: request.Clone()}, nil
}

func (record RequestRecord) Validate() error {
	if err := record.Request.Validate(); err != nil {
		return err
	}
	if record.Delivery != nil {
		if err := record.Delivery.Validate(); err != nil {
			return err
		}
		if !record.Delivery.matchesRequest(record.Request) {
			return ErrRequestConflict
		}
		if record.Request.Status != Pending && record.Request.ResolvedAt.Before(record.Delivery.CreatedAt) {
			return errors.New("secret request cannot settle before its reserved delivery")
		}
	}
	if record.Request.Status != Fulfilled {
		if record.Installation != nil {
			return errors.New("unfulfilled secret request must not have an installation")
		}
		return nil
	}
	if record.Delivery == nil || record.Installation == nil {
		return errors.New("fulfilled secret request requires its delivery and installation")
	}
	if err := record.Installation.Validate(); err != nil {
		return err
	}
	ack, delivery, fulfillment := record.Installation, record.Delivery, record.Request.Fulfillment
	if !ack.matchesDelivery(*delivery) || fulfillment.DeliveryID != delivery.ID ||
		fulfillment.SecretID != ack.SecretID || fulfillment.SecretVersion != ack.SecretVersion ||
		record.Request.ResolvedAt.Before(ack.InstalledAt) || !record.Request.ResolvedAt.Before(delivery.ExpiresAt) {
		return ErrRequestConflict
	}
	return nil
}

func (record RequestRecord) Clone() RequestRecord {
	record.Request = record.Request.Clone()
	if record.Delivery != nil {
		delivery := record.Delivery.Clone()
		record.Delivery = &delivery
	}
	if record.Installation != nil {
		installation := *record.Installation
		record.Installation = &installation
	}
	return record
}

// MatchesRequest compares the original pending request, ignoring saved progress.
// A retry cannot change its identity, key, recipient, creation time or deadline.
func (record RequestRecord) MatchesRequest(request Request) bool {
	if request.Validate() != nil || request.Status != Pending {
		return false
	}
	saved := record.Request
	return saved.ID == request.ID && saved.Key == request.Key && saved.AgentID == request.AgentID &&
		saved.RequestedAt.Equal(request.RequestedAt) &&
		saved.ExpiresAt.Equal(request.ExpiresAt)
}

func (record RequestRecord) WithDelivery(delivery Delivery) (RequestRecord, error) {
	if err := record.Validate(); err != nil {
		return RequestRecord{}, err
	}
	if err := delivery.Validate(); err != nil {
		return RequestRecord{}, err
	}
	if record.Delivery != nil {
		if record.Delivery.equal(delivery) {
			return record.Clone(), nil
		}
		return RequestRecord{}, ErrRequestConflict
	}
	if record.Request.Status != Pending {
		return RequestRecord{}, ErrRequestClosed
	}
	if !delivery.matchesRequest(record.Request) {
		return RequestRecord{}, ErrRequestConflict
	}
	record = record.Clone()
	delivery = delivery.Clone()
	record.Delivery = &delivery
	return record, nil
}

// Acknowledge verifies saved delivery evidence and trusted receipt time. It does
// not check a separate SecretStore, sender authorization or the live agent's
// current request. Hosts must check those before using fulfillment for readiness.
func (record RequestRecord) Acknowledge(installation Installation, now time.Time) (RequestRecord, error) {
	if err := record.Validate(); err != nil {
		return RequestRecord{}, err
	}
	if err := installation.Validate(); err != nil {
		return RequestRecord{}, err
	}
	if !validTime(now) || now.Before(installation.InstalledAt) {
		return RequestRecord{}, errors.New("secret acknowledgement requires a valid receipt time at or after installation")
	}
	if record.Installation != nil {
		if record.Installation.equal(installation) {
			return record.Clone(), nil
		}
		return RequestRecord{}, ErrRequestConflict
	}
	if record.Delivery == nil || !installation.matchesDelivery(*record.Delivery) {
		return RequestRecord{}, ErrRequestConflict
	}
	if err := record.Request.pendingAt(now); err != nil {
		return RequestRecord{}, err
	}
	if !now.Before(record.Delivery.ExpiresAt) {
		return RequestRecord{}, ErrRequestExpired
	}
	request, err := record.Request.Fulfill(record.Delivery.Secret.Metadata, record.Delivery.ID, now)
	if err != nil {
		return RequestRecord{}, err
	}
	record = record.Clone()
	record.Request, record.Installation = request, &installation
	return record, nil
}

func (record RequestRecord) Cancel(now time.Time) (RequestRecord, error) {
	if err := record.Validate(); err != nil {
		return RequestRecord{}, err
	}
	request, err := record.Request.Cancel(now)
	if err != nil {
		return RequestRecord{}, err
	}
	record = record.Clone()
	record.Request = request
	return record, record.Validate()
}

func (record RequestRecord) Expire(now time.Time) (RequestRecord, error) {
	if err := record.Validate(); err != nil {
		return RequestRecord{}, err
	}
	request, err := record.Request.Expire(now)
	if err != nil {
		return RequestRecord{}, err
	}
	record = record.Clone()
	record.Request = request
	return record, nil
}
