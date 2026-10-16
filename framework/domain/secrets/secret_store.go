package secrets

import (
	"context"
	"errors"
)

var (
	ErrUnknownSecret  = errors.New("secret does not exist")
	ErrSecretConflict = errors.New("secret identity or version conflicts with saved state")
	ErrWriteUncertain = errors.New("secret write outcome is uncertain; reread before retrying")
)

// MetadataStore exposes inventory without giving controllers access to encrypted
// values. GetMetadata returns ErrUnknownSecret when absent. List returns metadata
// in the exact scope, sorted by key name, including expired secrets. Callers derive
// availability using AvailabilityAt; reads never delete expired records.
type MetadataStore interface {
	GetMetadata(ctx context.Context, key Key) (Metadata, error)
	List(ctx context.Context, scope Scope) ([]Metadata, error)
}

// SecretStore persists only encrypted records. Implementations validate inputs,
// support concurrent use and context cancellation, and return independent copies.
// Writes apply atomically to the latest record. Except for ErrWriteUncertain,
// errors leave saved state unchanged; callers reconcile uncertain writes by reading
// current state before retrying. Stores never encrypt, decrypt or hold key material.
// Provider locks, transactions and storage revisions stay behind this interface.
type SecretStore interface {
	MetadataStore

	// Create requires version 1 with equal creation/update times and unique key
	// and ID. An identical replay of a still-present record succeeds; another
	// record or a retired ID returns ErrSecretConflict. Providers retain retired
	// identities so a delayed Create cannot resurrect a deleted secret.
	Create(ctx context.Context, secret Secret) error

	// Get returns the encrypted record, or ErrUnknownSecret. Use GetMetadata
	// when a caller needs only inventory or availability.
	Get(ctx context.Context, key Key) (Secret, error)

	// Replace requires the saved version to equal expectedVersion. It preserves
	// ID, key and creation time, requires secret.Version == expectedVersion+1,
	// and cannot move update time backwards. An identical replay of an already
	// applied replacement succeeds. Other stale/conflicting writes return
	// ErrSecretConflict; an absent key returns ErrUnknownSecret. Version overflow
	// must fail.
	Replace(ctx context.Context, secret Secret, expectedVersion uint64) error

	// Delete requires the exact saved ID and version, preventing deletion of a
	// replacement or a recreated secret. A mismatch, including an ID belonging
	// to another key, returns ErrSecretConflict. Deletion retires the ID atomically
	// with removing the record from reads. An absent key also retires the supplied
	// ID and succeeds, preventing a delayed initial Create after an uncertain write.
	// A later Create uses a fresh UUID; old deliveries and fulfillments must never
	// match that new identity.
	Delete(ctx context.Context, key Key, secretID string, expectedVersion uint64) error
}
