package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sort"
	"strings"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	"github.com/defos-ai/commander/providers/fs/internal/jsonfile"
)

// One persistent OS lock protects both global ID and scoped-name uniqueness;
// deletion and identity retirement commit through the same atomic replacement.
type SecretFsStore struct{ file *jsonfile.Collection }

var _ secretmodel.SecretStore = (*SecretFsStore)(nil)

func NewSecretFsStore(root string) (*SecretFsStore, error) {
	if root == "" {
		root = filepath.Join("state", "database")
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("secret database root must not be blank")
	}
	if err := jsonfile.PrepareDirectory(root); err != nil {
		return nil, err
	}
	file, err := jsonfile.New(filepath.Join(root, "secrets.json"))
	if err != nil {
		return nil, writeError(err)
	}
	store := &SecretFsStore{file: file}
	if err := store.view(context.Background(), func([]storedRecord) error { return nil }); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SecretFsStore) Create(ctx context.Context, secret secretmodel.Secret) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := secret.Validate(); err != nil {
		return err
	}
	if secret.Metadata.Version != 1 || !secret.Metadata.CreatedAt.Equal(secret.Metadata.UpdatedAt) {
		return errors.New("new secret must have version 1 and equal creation and update times")
	}
	snapshot := secret.Clone()
	return s.update(ctx, func(records []storedRecord) ([]storedRecord, error) {
		for _, record := range records {
			identity := record.identity()
			if identity.ID == snapshot.Metadata.ID {
				if record.Secret != nil && equalSecrets(*record.Secret, snapshot) {
					return records, nil
				}
				return nil, secretmodel.ErrSecretConflict
			}
			if record.Secret != nil && identity.Key == snapshot.Metadata.Key {
				return nil, secretmodel.ErrSecretConflict
			}
		}
		return append(records, storedRecord{Secret: &snapshot}), nil
	})
}

func (s *SecretFsStore) Get(ctx context.Context, key secretmodel.Key) (secretmodel.Secret, error) {
	if err := checkContext(ctx); err != nil {
		return secretmodel.Secret{}, err
	}
	if err := key.Validate(); err != nil {
		return secretmodel.Secret{}, err
	}
	var result secretmodel.Secret
	err := s.view(ctx, func(records []storedRecord) error {
		index := findActive(records, key)
		if index < 0 {
			return secretmodel.ErrUnknownSecret
		}
		result = *records[index].Secret
		return nil
	})
	if err != nil {
		return secretmodel.Secret{}, err
	}
	return result, nil
}

func (s *SecretFsStore) GetMetadata(ctx context.Context, key secretmodel.Key) (secretmodel.Metadata, error) {
	secret, err := s.Get(ctx, key)
	return secret.Metadata, err
}

func (s *SecretFsStore) List(ctx context.Context, scope secretmodel.Scope) ([]secretmodel.Metadata, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	result := []secretmodel.Metadata{}
	err := s.view(ctx, func(records []storedRecord) error {
		for _, record := range records {
			if record.Secret != nil && record.Secret.Metadata.Key.Scope == scope {
				result = append(result, record.Secret.Metadata)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key.Name < result[j].Key.Name })
	return result, nil
}

func (s *SecretFsStore) Replace(ctx context.Context, secret secretmodel.Secret, expectedVersion uint64) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := secret.Validate(); err != nil {
		return err
	}
	if expectedVersion == 0 || expectedVersion == math.MaxUint64 || secret.Metadata.Version != expectedVersion+1 {
		return secretmodel.ErrSecretConflict
	}
	snapshot := secret.Clone()
	return s.update(ctx, func(records []storedRecord) ([]storedRecord, error) {
		index := findActive(records, snapshot.Metadata.Key)
		if index < 0 {
			return nil, secretmodel.ErrUnknownSecret
		}
		saved := records[index].Secret
		if equalSecrets(*saved, snapshot) {
			return records, nil
		}
		if saved.Metadata.ID != snapshot.Metadata.ID || saved.Metadata.Version != expectedVersion ||
			!saved.Metadata.CreatedAt.Equal(snapshot.Metadata.CreatedAt) || snapshot.Metadata.UpdatedAt.Before(saved.Metadata.UpdatedAt) {
			return nil, secretmodel.ErrSecretConflict
		}
		records[index].Secret = &snapshot
		return records, nil
	})
}

func (s *SecretFsStore) Delete(ctx context.Context, key secretmodel.Key, secretID string, expectedVersion uint64) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	if !id.Valid(secretID) || expectedVersion == 0 {
		return errors.New("secret deletion requires a canonical UUID and positive version")
	}
	return s.update(ctx, func(records []storedRecord) ([]storedRecord, error) {
		knownID := false
		for _, record := range records {
			identity := record.identity()
			if identity.ID == secretID {
				if identity.Key != key {
					return nil, secretmodel.ErrSecretConflict
				}
				knownID = true
			}
		}
		retired := storedRecord{Retired: &retiredSecret{ID: secretID, Key: key}}
		if index := findActive(records, key); index >= 0 {
			metadata := records[index].Secret.Metadata
			if metadata.ID != secretID || metadata.Version != expectedVersion {
				return nil, secretmodel.ErrSecretConflict
			}
			records[index] = retired
		} else if !knownID {
			// Retire even an absent ID: its delayed initial Create must fail.
			records = append(records, retired)
		}
		return records, nil
	})
}

func (s *SecretFsStore) view(ctx context.Context, fn func([]storedRecord) error) error {
	return s.file.View(ctx, func(data []byte) error {
		records, err := decode(data)
		if err != nil {
			return err
		}
		return fn(records)
	})
}

func (s *SecretFsStore) update(ctx context.Context, fn func([]storedRecord) ([]storedRecord, error)) error {
	err := s.file.Update(ctx, func(data []byte) ([]byte, error) {
		records, err := decode(data)
		if err != nil {
			return nil, err
		}
		records, err = fn(records)
		if err != nil {
			return nil, err
		}
		// Identical retries also publish and sync, confirming an uncertain write.
		return json.Marshal(records)
	})
	return writeError(err)
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("secret database requires a context")
	}
	return ctx.Err()
}

func writeError(err error) error {
	if errors.Is(err, jsonfile.ErrWriteUncertain) {
		return errors.Join(secretmodel.ErrWriteUncertain, err)
	}
	return err
}
