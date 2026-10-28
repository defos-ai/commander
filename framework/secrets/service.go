package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/defos-ai/commander/framework/domain"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

// Encryptor binds plaintext to the supplied metadata. It must return encrypted
// bytes only, preserving the metadata unchanged.
type Encryptor interface {
	Encrypt(secretmodel.Metadata, []byte) (secretmodel.Secret, error)
}

// Service owns secret identities and versions. It neither decrypts values nor
// fulfills secret requests; storing a value does not establish delivery.
// Create and Replace return attempted metadata alongside ErrWriteUncertain so
// callers can reconcile identity/version. That metadata does not confirm a save.
type Service struct {
	store     secretmodel.SecretStore
	encryptor Encryptor
}

func NewService(store secretmodel.SecretStore, encryptor Encryptor) (*Service, error) {
	if store == nil || encryptor == nil {
		return nil, errors.New("secret store and encryptor are required")
	}
	return &Service{store: store, encryptor: encryptor}, nil
}

func (s *Service) Create(ctx context.Context, key secretmodel.Key, value []byte, expiresAt time.Time) (secretmodel.Metadata, error) {
	if err := validateContext(ctx); err != nil {
		return secretmodel.Metadata{}, err
	}
	if err := key.Validate(); err != nil {
		return secretmodel.Metadata{}, invalidInput(err)
	}
	now := time.Now().UTC()
	metadata := secretmodel.Metadata{
		ID: id.New(), Key: key, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: expiresAt.UTC(),
	}
	if err := metadata.Validate(); err != nil {
		return secretmodel.Metadata{}, invalidInput(err)
	}
	encrypted, err := s.encrypt(ctx, metadata, value)
	if err != nil {
		return secretmodel.Metadata{}, err
	}
	return writeResult(metadata, s.store.Create(ctx, encrypted))
}

func (s *Service) Replace(ctx context.Context, key secretmodel.Key, secretID string, expectedVersion uint64, value []byte, expiresAt time.Time) (secretmodel.Metadata, error) {
	if err := validateMutation(ctx, key, secretID, expectedVersion); err != nil {
		return secretmodel.Metadata{}, err
	}
	if expectedVersion == math.MaxUint64 {
		return secretmodel.Metadata{}, secretmodel.ErrSecretConflict
	}
	metadata, err := s.store.GetMetadata(ctx, key)
	if err != nil {
		return secretmodel.Metadata{}, err
	}
	if metadata.ID != secretID || metadata.Version != expectedVersion || metadata.Key != key {
		return secretmodel.Metadata{}, secretmodel.ErrSecretConflict
	}
	now := time.Now().UTC()
	if now.Before(metadata.UpdatedAt) {
		return secretmodel.Metadata{}, secretmodel.ErrSecretConflict
	}
	metadata.Version++
	metadata.UpdatedAt = now
	metadata.ExpiresAt = expiresAt.UTC()
	if err := metadata.Validate(); err != nil {
		return secretmodel.Metadata{}, invalidInput(err)
	}
	encrypted, err := s.encrypt(ctx, metadata, value)
	if err != nil {
		return secretmodel.Metadata{}, err
	}
	return writeResult(metadata, s.store.Replace(ctx, encrypted, expectedVersion))
}

func (s *Service) Delete(ctx context.Context, key secretmodel.Key, secretID string, expectedVersion uint64) error {
	if err := validateMutation(ctx, key, secretID, expectedVersion); err != nil {
		return err
	}
	// The store also retires a missing identity. Reading first would lose that
	// protection against a delayed Create after an uncertain write.
	return s.store.Delete(ctx, key, secretID, expectedVersion)
}

func (s *Service) GetMetadata(ctx context.Context, key secretmodel.Key) (secretmodel.Metadata, error) {
	if err := validateContext(ctx); err != nil {
		return secretmodel.Metadata{}, err
	}
	if err := key.Validate(); err != nil {
		return secretmodel.Metadata{}, invalidInput(err)
	}
	return s.store.GetMetadata(ctx, key)
}

func (s *Service) List(ctx context.Context) ([]secretmodel.Metadata, error) {
	if err := validateContext(ctx); err != nil {
		return nil, err
	}
	return s.store.List(ctx)
}

func (s *Service) encrypt(ctx context.Context, metadata secretmodel.Metadata, value []byte) (secretmodel.Secret, error) {
	if err := validateContext(ctx); err != nil {
		return secretmodel.Secret{}, err
	}
	plaintext := bytes.Clone(value)
	defer clear(plaintext)
	encrypted, err := s.encryptor.Encrypt(metadata, plaintext)
	if err != nil {
		return secretmodel.Secret{}, err
	}
	if encrypted.Metadata != metadata || encrypted.Validate() != nil {
		return secretmodel.Secret{}, errors.New("encryptor returned an invalid secret record")
	}
	if err := ctx.Err(); err != nil {
		return secretmodel.Secret{}, err
	}
	return encrypted.Clone(), nil
}

func validateMutation(ctx context.Context, key secretmodel.Key, secretID string, expectedVersion uint64) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return invalidInput(err)
	}
	if !id.Valid(secretID) {
		return invalidInput(errors.New("secret ID must be a canonical UUID"))
	}
	if expectedVersion == 0 {
		return secretmodel.ErrSecretConflict
	}
	return nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return invalidInput(errors.New("context is required"))
	}
	return ctx.Err()
}

func invalidInput(err error) error {
	return fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
}

// Preserve the attempted identity for reconciliation when publication may have
// succeeded. Never automatically retry a write with a new identity or nonce.
func writeResult(metadata secretmodel.Metadata, err error) (secretmodel.Metadata, error) {
	if err != nil && !errors.Is(err, secretmodel.ErrWriteUncertain) {
		return secretmodel.Metadata{}, err
	}
	return metadata, err
}
