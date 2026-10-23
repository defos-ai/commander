package secrets_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
)

func TestSecretServiceLifecycle(t *testing.T) {
	store, cipher, service := newServiceFixture(t)
	ctx, key := context.Background(), secretFixture().Metadata.Key
	plaintext := []byte("sensitive-service-fixture")
	expires := time.Now().Add(time.Hour)
	before := time.Now()
	created, err := service.Create(ctx, key, plaintext, expires)
	if err != nil {
		t.Fatal(err)
	}
	if !id.Valid(created.ID) || created.Version != 1 || created.Key != key ||
		!created.CreatedAt.Equal(created.UpdatedAt) || created.CreatedAt.Before(before) ||
		created.UpdatedAt.After(time.Now()) || created.CreatedAt.Location() != time.UTC || !created.ExpiresAt.Equal(expires) {
		t.Fatal("create did not assign identity, scope, version and server timestamps")
	}
	if bytes.Contains(store.record.Value.Ciphertext, plaintext) {
		t.Fatal("store received plaintext")
	}
	plaintext[0] = 'X'
	decrypted, err := cipher.Decrypt(store.record)
	if err != nil || string(decrypted) != "sensitive-service-fixture" {
		t.Fatal("stored ciphertext is not independent of the caller's buffer")
	}
	if metadata, err := service.GetMetadata(ctx, key); err != nil || metadata != created {
		t.Fatalf("get metadata: %v", err)
	}
	if values, err := service.List(ctx, key.Scope); err != nil || len(values) != 1 || values[0] != created {
		t.Fatalf("list metadata: %v", err)
	}

	// Explicitly empty values are allowed; HTTP presence checks belong to adapters.
	replaced, err := service.Replace(ctx, key, created.ID, created.Version, []byte{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != created.ID || replaced.Version != 2 || !replaced.CreatedAt.Equal(created.CreatedAt) ||
		replaced.UpdatedAt.Before(created.UpdatedAt) || !replaced.ExpiresAt.IsZero() {
		t.Fatal("replace did not preserve identity/creation or clear expiration")
	}
	if decrypted, err := cipher.Decrypt(store.record); err != nil || len(decrypted) != 0 {
		t.Fatalf("replacement is not valid authenticated empty plaintext: %v", err)
	}
	if err := service.Delete(ctx, key, created.ID, 1); !errors.Is(err, secretmodel.ErrSecretConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := service.Delete(ctx, key, created.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetMetadata(ctx, key); !errors.Is(err, secretmodel.ErrUnknownSecret) {
		t.Fatalf("deleted metadata: %v", err)
	}
	recreated, err := service.Create(ctx, key, nil, time.Time{})
	if err != nil || recreated.ID == created.ID || recreated.Version != 1 {
		t.Fatalf("recreate did not allocate a fresh identity: %v", err)
	}
	if _, err := service.Replace(ctx, key, created.ID, 1, nil, time.Time{}); !errors.Is(err, secretmodel.ErrSecretConflict) {
		t.Fatalf("replace after same-key recreation: %v", err)
	}
}

func TestSecretServiceRejectsStaleReplacementBeforeEncryption(t *testing.T) {
	for _, scenario := range []string{"missing", "wrong ID", "stale version", "zero version", "overflow", "clock behind saved update"} {
		t.Run(scenario, func(t *testing.T) {
			store, _, _ := newServiceFixture(t)
			key := secretFixture().Metadata.Key
			store.record = secretFixture()
			store.record.Metadata.CreatedAt = time.Now().Add(-2 * time.Hour)
			store.record.Metadata.UpdatedAt = time.Now().Add(-time.Hour)
			store.record.Metadata.ExpiresAt = time.Time{}
			identity, version, want := store.record.Metadata.ID, store.record.Metadata.Version, secretmodel.ErrSecretConflict
			switch scenario {
			case "missing":
				store.record = secretmodel.Secret{}
				want = secretmodel.ErrUnknownSecret
			case "wrong ID":
				identity = id.New()
			case "stale version":
				version--
			case "zero version":
				version = 0
			case "overflow":
				version = math.MaxUint64
				store.record.Metadata.Version = version
			case "clock behind saved update":
				store.record.Metadata.UpdatedAt = time.Now().Add(time.Hour)
			}
			service, err := secretservice.NewService(store, encryptFunc(func(secretmodel.Metadata, []byte) (secretmodel.Secret, error) {
				t.Fatal("invalid replacement reached encryption")
				return secretmodel.Secret{}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Replace(context.Background(), key, identity, version, []byte("new value"), time.Time{}); !errors.Is(err, want) {
				t.Fatalf("replacement error = %v, want %v", err, want)
			}
			if store.writes != 0 {
				t.Fatal("invalid replacement reached persistence")
			}
		})
	}
}

func TestSecretServiceCASRejectsUpdateAfterMetadataRead(t *testing.T) {
	store, cipher, service := newServiceFixture(t)
	key, ctx := secretFixture().Metadata.Key, context.Background()
	created, err := service.Create(ctx, key, []byte("first value"), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	service, err = secretservice.NewService(store, encryptFunc(func(metadata secretmodel.Metadata, plaintext []byte) (secretmodel.Secret, error) {
		// A different caller replaces the value after our metadata read.
		newer := created
		newer.Version++
		newer.UpdatedAt = metadata.UpdatedAt
		competing, err := cipher.Encrypt(newer, []byte("competing value"))
		if err != nil {
			return secretmodel.Secret{}, err
		}
		if err := store.Replace(ctx, competing, created.Version); err != nil {
			return secretmodel.Secret{}, err
		}
		return cipher.Encrypt(metadata, plaintext)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Replace(ctx, key, created.ID, created.Version, []byte("stale value"), time.Time{}); !errors.Is(err, secretmodel.ErrSecretConflict) {
		t.Fatalf("concurrent replacement: %v", err)
	}
	if value, err := cipher.Decrypt(store.record); err != nil || string(value) != "competing value" {
		t.Fatal("stale service write replaced the concurrent winner")
	}
}

func TestSecretServiceValidationHasNoSideEffects(t *testing.T) {
	store, cipher, _ := newServiceFixture(t)
	service, err := secretservice.NewService(store, encryptFunc(func(secretmodel.Metadata, []byte) (secretmodel.Secret, error) {
		t.Fatal("invalid input reached encryption")
		return secretmodel.Secret{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secretservice.NewService(nil, cipher); err == nil {
		t.Fatal("missing store accepted")
	}
	if _, err := secretservice.NewService(store, nil); err == nil {
		t.Fatal("missing encryptor accepted")
	}
	key := secretFixture().Metadata.Key
	operations := map[string]func(context.Context, secretmodel.Key) error{
		"create": func(ctx context.Context, key secretmodel.Key) error {
			_, err := service.Create(ctx, key, nil, time.Time{})
			return err
		},
		"replace": func(ctx context.Context, key secretmodel.Key) error {
			_, err := service.Replace(ctx, key, id.New(), 1, nil, time.Time{})
			return err
		},
		"delete": func(ctx context.Context, key secretmodel.Key) error { return service.Delete(ctx, key, id.New(), 1) },
		"metadata": func(ctx context.Context, key secretmodel.Key) error {
			_, err := service.GetMetadata(ctx, key)
			return err
		},
		"list": func(ctx context.Context, key secretmodel.Key) error {
			_, err := service.List(ctx, key.Scope)
			return err
		},
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, call := range operations {
		t.Run(name, func(t *testing.T) {
			if err := call(nil, key); !errors.Is(err, domain.ErrInvalidInput) {
				t.Fatalf("nil context: %v", err)
			}
			if err := call(cancelled, key); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled context: %v", err)
			}
			invalid := key
			invalid.Scope.AgentType = "bad/value-that-must-not-appear-in-errors"
			err := call(context.Background(), invalid)
			if !errors.Is(err, domain.ErrInvalidInput) || strings.Contains(err.Error(), invalid.Scope.AgentType) {
				t.Fatalf("invalid scope was accepted or disclosed: %v", err)
			}
		})
	}
	if store.reads != 0 || store.writes != 0 {
		t.Fatal("invalid inputs reached persistence")
	}
	if _, err := service.Create(context.Background(), key, nil, time.Now().Add(-time.Minute)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expired creation: %v", err)
	}
	if _, err := service.Replace(context.Background(), key, "invalid-ID", 1, nil, time.Time{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("invalid replacement identity: %v", err)
	}
	if err := service.Delete(context.Background(), key, "invalid-ID", 1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("invalid deletion identity: %v", err)
	}
	store.record = secretFixture()
	store.record.Metadata.CreatedAt = time.Now().Add(-2 * time.Hour)
	store.record.Metadata.UpdatedAt = time.Now().Add(-time.Hour)
	if _, err := service.Replace(context.Background(), key, store.record.Metadata.ID, 2, nil, time.Now().Add(-time.Minute)); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("expired replacement: %v", err)
	}
}

func TestSecretServiceEncryptionBoundary(t *testing.T) {
	for _, scenario := range []string{"failure", "changed metadata", "invalid envelope", "cancelled after encryption", "input mutation"} {
		t.Run(scenario, func(t *testing.T) {
			store, cipher, _ := newServiceFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("encryptor failed")
			plaintext := []byte("independent-value")
			var received []byte
			service, err := secretservice.NewService(store, encryptFunc(func(metadata secretmodel.Metadata, value []byte) (secretmodel.Secret, error) {
				received = value
				if scenario == "failure" {
					return secretmodel.Secret{}, failure
				}
				if scenario == "input mutation" {
					value[0] = 'X'
				}
				encrypted, err := cipher.Encrypt(metadata, value)
				switch scenario {
				case "changed metadata":
					encrypted.Metadata.Version++
				case "invalid envelope":
					encrypted.Value.Ciphertext = nil
				case "cancelled after encryption":
					cancel()
				}
				return encrypted, err
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Create(ctx, secretFixture().Metadata.Key, plaintext, time.Time{})
			if scenario == "input mutation" {
				if err != nil || store.writes != 1 || string(plaintext) != "independent-value" {
					t.Fatalf("encryptor mutated caller memory: %v", err)
				}
			} else if err == nil || store.writes != 0 {
				t.Fatalf("invalid encryption result reached persistence: %v", err)
			}
			if scenario == "failure" && !errors.Is(err, failure) {
				t.Fatal("encryption failure not preserved")
			}
			if scenario == "cancelled after encryption" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation not preserved")
			}
			if !bytes.Equal(received, make([]byte, len(received))) {
				t.Fatal("temporary plaintext buffer was retained")
			}
		})
	}
}

func TestSecretServiceStoreFailuresAreNotRetried(t *testing.T) {
	failure := errors.New("storage failed")
	for _, uncertain := range []bool{false, true} {
		for _, operation := range []string{"create", "replace", "delete"} {
			t.Run(operation+"/uncertain="+map[bool]string{true: "true", false: "false"}[uncertain], func(t *testing.T) {
				store, _, service := newServiceFixture(t)
				ctx, key := context.Background(), secretFixture().Metadata.Key
				created, err := service.Create(ctx, key, []byte("original"), time.Time{})
				if err != nil {
					t.Fatal(err)
				}
				store.writes = 0
				store.writeErr = failure
				if uncertain {
					store.writeErr = errors.Join(secretmodel.ErrWriteUncertain, failure)
				}
				var attempted secretmodel.Metadata
				switch operation {
				case "create":
					store.record = secretmodel.Secret{}
					attempted, err = service.Create(ctx, key, nil, time.Time{})
				case "replace":
					attempted, err = service.Replace(ctx, key, created.ID, created.Version, nil, time.Time{})
				case "delete":
					err = service.Delete(ctx, key, created.ID, created.Version)
				}
				if !errors.Is(err, failure) || errors.Is(err, secretmodel.ErrWriteUncertain) != uncertain || store.writes != 1 {
					t.Fatalf("store failure was lost or retried: writes=%d, error=%v", store.writes, err)
				}
				if operation != "delete" {
					if uncertain && (!id.Valid(attempted.ID) || attempted.Key != key) {
						t.Fatal("uncertain write lost attempted metadata needed for reconciliation")
					}
					if !uncertain && attempted != (secretmodel.Metadata{}) {
						t.Fatal("failed write returned success metadata")
					}
				}
			})
		}
	}
	store, _, service := newServiceFixture(t)
	store.readErr = failure
	ctx, key := context.Background(), secretFixture().Metadata.Key
	if _, err := service.GetMetadata(ctx, key); !errors.Is(err, failure) {
		t.Fatalf("metadata failure: %v", err)
	}
	if _, err := service.List(ctx, key.Scope); !errors.Is(err, failure) {
		t.Fatalf("list failure: %v", err)
	}
	if _, err := service.Replace(ctx, key, id.New(), 1, nil, time.Time{}); !errors.Is(err, failure) {
		t.Fatalf("replacement read failure: %v", err)
	}
	if store.writes != 0 || store.reads != 3 {
		t.Fatal("failed reads triggered writes or retries")
	}
}

func TestSecretServiceDeleteRetiresAbsentIdentity(t *testing.T) {
	store, _, service := newServiceFixture(t)
	key, identity := secretFixture().Metadata.Key, id.New()
	if err := service.Delete(context.Background(), key, identity, 1); err != nil {
		t.Fatal(err)
	}
	if store.reads != 0 || store.writes != 1 || store.retired[identity] != key {
		t.Fatal("delete failed to delegate absent identity retirement directly to store")
	}
}

type encryptFunc func(secretmodel.Metadata, []byte) (secretmodel.Secret, error)

func (f encryptFunc) Encrypt(metadata secretmodel.Metadata, plaintext []byte) (secretmodel.Secret, error) {
	return f(metadata, plaintext)
}

func newServiceFixture(t *testing.T) (*serviceStore, *crypto.SecretCipher, *secretservice.Service) {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &serviceStore{retired: make(map[string]secretmodel.Key)}
	service, err := secretservice.NewService(store, cipher)
	if err != nil {
		t.Fatal(err)
	}
	return store, cipher, service
}

// Each test owns this store. Atomic synchronous mutations allow scripted updates
// between reads and writes without timing-dependent goroutines.
type serviceStore struct {
	record            secretmodel.Secret
	retired           map[string]secretmodel.Key
	reads, writes     int
	readErr, writeErr error
}

func (s *serviceStore) Create(ctx context.Context, record secretmodel.Secret) error {
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

func (s *serviceStore) Get(context.Context, secretmodel.Key) (secretmodel.Secret, error) {
	panic("management service must not read encrypted values")
}

func (s *serviceStore) GetMetadata(ctx context.Context, key secretmodel.Key) (secretmodel.Metadata, error) {
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

func (s *serviceStore) List(ctx context.Context, scope secretmodel.Scope) ([]secretmodel.Metadata, error) {
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

func (s *serviceStore) Replace(ctx context.Context, record secretmodel.Secret, expectedVersion uint64) error {
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

func (s *serviceStore) Delete(ctx context.Context, key secretmodel.Key, identity string, expectedVersion uint64) error {
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
