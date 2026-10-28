package fakes

import (
	"context"
	"math"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
)

type EncryptFunc func(secretmodel.Metadata, []byte) (secretmodel.Secret, error)

func (f EncryptFunc) Encrypt(metadata secretmodel.Metadata, plaintext []byte) (secretmodel.Secret, error) {
	return f(metadata, plaintext)
}

// Each test owns this store. Atomic synchronous mutations allow scripted updates
// between reads and writes without timing-dependent goroutines.
type ServiceStore struct {
	record            secretmodel.Secret
	retired           map[string]secretmodel.Key
	reads, writes     int
	readErr, writeErr error
}

func (s *ServiceStore) Create(ctx context.Context, record secretmodel.Secret) error {
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if s.record.Metadata.ID != "" || s.retired[record.Metadata.ID] != (secretmodel.Key{}) {
		return secretmodel.ErrSecretConflict
	}
	s.record = record.Clone()
	return nil
}

func (s *ServiceStore) Get(context.Context, secretmodel.Key) (secretmodel.Secret, error) {
	panic("management service must not read encrypted values")
}

func (s *ServiceStore) GetMetadata(ctx context.Context, key secretmodel.Key) (secretmodel.Metadata, error) {
	s.reads++
	if s.readErr != nil {
		return secretmodel.Metadata{}, s.readErr
	}
	if err := ctx.Err(); err != nil {
		return secretmodel.Metadata{}, err
	}
	if s.record.Metadata.ID == "" || s.record.Metadata.Key != key {
		return secretmodel.Metadata{}, secretmodel.ErrUnknownSecret
	}
	return s.record.Metadata, nil
}

func (s *ServiceStore) List(ctx context.Context, scope secretmodel.Scope) ([]secretmodel.Metadata, error) {
	s.reads++
	if s.readErr != nil {
		return nil, s.readErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.record.Metadata.ID != "" && s.record.Metadata.Key.Scope == scope {
		return []secretmodel.Metadata{s.record.Metadata}, nil
	}
	return []secretmodel.Metadata{}, nil
}

func (s *ServiceStore) Replace(ctx context.Context, record secretmodel.Secret, expectedVersion uint64) error {
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	current := s.record.Metadata
	if current.ID == "" {
		return secretmodel.ErrUnknownSecret
	}
	if current.ID != record.Metadata.ID || current.Key != record.Metadata.Key || current.Version != expectedVersion ||
		expectedVersion == math.MaxUint64 || record.Metadata.Version != expectedVersion+1 ||
		!record.Metadata.CreatedAt.Equal(current.CreatedAt) || record.Metadata.UpdatedAt.Before(current.UpdatedAt) {
		return secretmodel.ErrSecretConflict
	}
	s.record = record.Clone()
	return nil
}

func (s *ServiceStore) Delete(ctx context.Context, key secretmodel.Key, identity string, expectedVersion uint64) error {
	s.writes++
	if s.writeErr != nil {
		return s.writeErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current := s.record.Metadata
	if current.ID != "" && (current.ID != identity || current.Key != key || current.Version != expectedVersion) {
		return secretmodel.ErrSecretConflict
	}
	if retired, ok := s.retired[identity]; ok && retired != key {
		return secretmodel.ErrSecretConflict
	}
	s.record = secretmodel.Secret{}
	s.retired[identity] = key
	return nil
}

func NewServiceStore() *ServiceStore                  { return &ServiceStore{retired: make(map[string]secretmodel.Key)} }
func (s *ServiceStore) Snapshot() secretmodel.Secret  { return s.record.Clone() }
func (s *ServiceStore) Set(record secretmodel.Secret) { s.record = record.Clone() }
func (s *ServiceStore) Change(change func(*secretmodel.Secret)) {
	record := s.Snapshot()
	change(&record)
	s.Set(record)
}
func (s *ServiceStore) Reads() int                              { return s.reads }
func (s *ServiceStore) Writes() int                             { return s.writes }
func (s *ServiceStore) ResetWrites()                            { s.writes = 0 }
func (s *ServiceStore) FailWrite(err error)                     { s.writeErr = err }
func (s *ServiceStore) FailRead(err error)                      { s.readErr = err }
func (s *ServiceStore) Retired(identity string) secretmodel.Key { return s.retired[identity] }
