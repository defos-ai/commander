package secrets

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUnknownRequest  = errors.New("secret request does not exist")
	ErrRequestConflict = errors.New("secret request or delivery conflicts with saved state")
)

// RequestStore atomically tracks each request with its encrypted delivery and
// installation evidence. It never publishes, decrypts or authorizes messages.
// It checks the recorded delivery, not the current secret in a separate store;
// historical fulfillment is not proof of current agent readiness.
//
// Implementations validate inputs, honor contexts, return independent copies,
// and serialize changes across instances. IDs and terminal records are retained;
// no automatic expiry, deletion or reopening is permitted. Failed writes leave
// saved state unchanged except ErrWriteUncertain: reread before acting/retrying.
type RequestStore interface {
	// Create accepts pending requests only. An identical original request can be
	// retried after progress without resetting it; changed content conflicts.
	Create(ctx context.Context, request Request) error
	Get(ctx context.Context, requestID string) (RequestRecord, error)
	// ListPending is a recovery snapshot sorted by request ID. It includes overdue
	// pending requests so the controller can explicitly expire them.
	ListPending(ctx context.Context) ([]RequestRecord, error)
	// SaveDelivery reserves one immutable delivery before publication. Delivery
	// IDs are unique across requests; exact retries preserve the saved snapshot,
	// including after settlement. Replacing it requires a fresh request.
	SaveDelivery(ctx context.Context, delivery Delivery) error
	// Acknowledge accepts exact matching evidence before the request/delivery
	// deadline. now is the trusted receipt time. Exact saved ACK replays preserve
	// the first settlement, even after expiry; changed ACK content conflicts.
	Acknowledge(ctx context.Context, installation Installation, now time.Time) error
	Cancel(ctx context.Context, requestID string, now time.Time) error
	Expire(ctx context.Context, requestID string, now time.Time) error
}
