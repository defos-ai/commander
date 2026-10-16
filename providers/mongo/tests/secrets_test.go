package mongo_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretmongo "github.com/defos-ai/commander/providers/mongo/secrets"
	"github.com/defos-ai/commander/providers/mongo/store"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const mongoSecretEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const mongoSecretPlaintext = "private-test-token-never-store-as-plaintext"

func TestSecretMongoStoreEncryptedRoundTrip(t *testing.T) {
	database := openStore(t, "secrets")
	catalog := openSecretMongoStore(t, database)
	value := mongoSecretFixture(t, "API_TOKEN")
	value.Metadata.ExpiresAt = value.Metadata.UpdatedAt.Add(time.Hour + time.Nanosecond)
	value = encryptMongoSecret(t, value.Metadata)
	expected := value.Clone()
	if err := catalog.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	value.Value.Nonce[0] ^= 1
	value.Value.Ciphertext[0] ^= 1
	reopened := openSecretMongoStore(t, database)
	assertMongoSecret(t, reopened, expected)
	got, err := reopened.Get(t.Context(), expected.Metadata.Key)
	if err != nil {
		t.Fatal(err)
	}
	got.Value.Nonce[0] ^= 1
	got.Value.Ciphertext[0] ^= 1
	assertMongoSecret(t, catalog, expected)

	var documents []bson.Raw
	if err := database.Find(t.Context(), "secrets", bson.D{}, &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents) == 0 {
		t.Fatal("no encrypted record persisted")
	}
	keyBytes, _ := hex.DecodeString(mongoSecretEncryptionKey)
	for _, document := range documents {
		for _, sensitive := range [][]byte{[]byte(mongoSecretPlaintext), []byte(mongoSecretEncryptionKey), keyBytes} {
			if bytes.Contains(document, sensitive) {
				t.Fatal("persisted document exposed plaintext or key material")
			}
		}
	}
}

func TestSecretMongoStoreScopedInventory(t *testing.T) {
	catalog := openSecretMongoStore(t, openStore(t, "secrets"))
	first, last, lower := mongoSecretFixture(t, "API_TOKEN"), mongoSecretFixture(t, "Z_TOKEN"), mongoSecretFixture(t, "api_token")
	first.Metadata.ExpiresAt = first.Metadata.UpdatedAt.Add(time.Hour)
	first = encryptMongoSecret(t, first.Metadata)
	profile, agent := mongoSecretFixture(t, "API_TOKEN"), mongoSecretFixture(t, "API_TOKEN")
	profile.Metadata.Key.Scope.Profile = "production"
	agent.Metadata.Key.Scope.AgentType = "other-agent"
	for _, value := range []secrets.Secret{lower, last, first, encryptMongoSecret(t, profile.Metadata), encryptMongoSecret(t, agent.Metadata)} {
		if err := catalog.Create(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	items, err := catalog.List(t.Context(), first.Metadata.Key.Scope)
	if err != nil || !reflect.DeepEqual(items, []secrets.Metadata{first.Metadata, last.Metadata, lower.Metadata}) {
		t.Fatalf("inventory must be sorted and scoped: count=%d, error=%v", len(items), err)
	}
	if secrets.AvailabilityAt(&items[0], first.Metadata.ExpiresAt) != secrets.Expired {
		t.Fatal("inventory lost expiration")
	}
	items[0].Key.Name = "CHANGED"
	metadata, err := catalog.GetMetadata(t.Context(), first.Metadata.Key)
	if err != nil || metadata != first.Metadata {
		t.Fatalf("inventory mutation changed stored metadata: %v", err)
	}
	assertMongoSecret(t, catalog, first)
	assertMongoSecret(t, catalog, lower)
	missing := first.Metadata.Key
	missing.Name = "MISSING"
	if _, err := catalog.Get(t.Context(), missing); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("missing value: %v", err)
	}
	if _, err := catalog.GetMetadata(t.Context(), missing); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("missing metadata: %v", err)
	}
}

func TestSecretMongoStoreCreateIdentityAndReplay(t *testing.T) {
	catalog := openSecretMongoStore(t, openStore(t, "secrets"))
	initial := mongoSecretFixture(t, "API_TOKEN")
	if err := catalog.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	replay := mongoSecretOtherZone(initial)
	if err := catalog.Create(t.Context(), replay); err != nil {
		t.Fatalf("equal time instants must replay: %v", err)
	}
	for _, change := range []func(*secrets.Secret){
		func(s *secrets.Secret) { s.Metadata.ID = id.New() },
		func(s *secrets.Secret) { s.Metadata.Key.Scope.Profile = "other" },
		func(s *secrets.Secret) { s.Value.Ciphertext[0] ^= 1 },
	} {
		conflict := initial.Clone()
		change(&conflict)
		if err := catalog.Create(t.Context(), conflict); !errors.Is(err, secrets.ErrSecretConflict) {
			t.Fatalf("conflicting create: %v", err)
		}
	}
	invalid := mongoSecretFixture(t, "NEW_TOKEN")
	invalid.Metadata.Version = 2
	if err := catalog.Create(t.Context(), invalid); err == nil {
		t.Fatal("create accepted a noninitial version")
	}
	invalid.Metadata.Version = 1
	invalid.Metadata.UpdatedAt = invalid.Metadata.UpdatedAt.Add(time.Nanosecond)
	if err := catalog.Create(t.Context(), invalid); err == nil {
		t.Fatal("create accepted unequal creation and update times")
	}
	assertMongoSecret(t, catalog, initial)
}

func TestSecretMongoStoreReplaceVersionsAndReplay(t *testing.T) {
	database := openStore(t, "secrets")
	catalog := openSecretMongoStore(t, database)
	initial, other := mongoSecretFixture(t, "API_TOKEN"), mongoSecretFixture(t, "OTHER_TOKEN")
	for _, value := range []secrets.Secret{initial, other} {
		if err := catalog.Create(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	metadata := initial.Metadata
	metadata.Version++
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Second)
	saved := encryptMongoSecret(t, metadata)
	if err := catalog.Replace(t.Context(), saved, 1); err != nil {
		t.Fatal(err)
	}
	if err := openSecretMongoStore(t, database).Replace(t.Context(), mongoSecretOtherZone(saved), 1); err != nil {
		t.Fatalf("replacement replay: %v", err)
	}
	metadata.Version++
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
	for _, test := range []struct {
		name string
		edit func(*secrets.Metadata)
	}{
		{"changed ID", func(m *secrets.Metadata) { m.ID = id.New() }},
		{"changed key", func(m *secrets.Metadata) { m.Key = other.Metadata.Key }},
		{"changed creation", func(m *secrets.Metadata) { m.CreatedAt = m.CreatedAt.Add(-time.Second) }},
		{"backward update", func(m *secrets.Metadata) { m.UpdatedAt = initial.Metadata.UpdatedAt }},
		{"skipped version", func(m *secrets.Metadata) { m.Version++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := metadata
			test.edit(&invalid)
			if err := catalog.Replace(t.Context(), encryptMongoSecret(t, invalid), 2); !errors.Is(err, secrets.ErrSecretConflict) {
				t.Fatalf("invalid replacement: %v", err)
			}
		})
	}
	for _, version := range []uint64{0, math.MaxUint64} {
		if err := catalog.Replace(t.Context(), initial, version); err == nil {
			t.Fatalf("invalid expected version %d accepted", version)
		}
	}
	missing := metadata
	missing.Key.Name = "MISSING"
	if err := catalog.Replace(t.Context(), encryptMongoSecret(t, missing), 2); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("replacement created a missing key: %v", err)
	}
	assertMongoSecret(t, catalog, saved)
	latest := encryptMongoSecret(t, metadata)
	if err := catalog.Replace(t.Context(), latest, 2); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Replace(t.Context(), saved, 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("stale replay replaced a newer version: %v", err)
	}
	assertMongoSecret(t, openSecretMongoStore(t, database), latest)
	assertMongoSecret(t, catalog, other)
}

func TestSecretMongoStoreDeletionRetiresIdentity(t *testing.T) {
	database := openStore(t, "secrets")
	catalog := openSecretMongoStore(t, database)
	initial, other := mongoSecretFixture(t, "API_TOKEN"), mongoSecretFixture(t, "OTHER_TOKEN")
	for _, value := range []secrets.Secret{initial, other} {
		if err := catalog.Create(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	for _, wrongID := range []string{id.New(), other.Metadata.ID} {
		if err := catalog.Delete(t.Context(), initial.Metadata.Key, wrongID, 1); !errors.Is(err, secrets.ErrSecretConflict) {
			t.Fatalf("wrong ID deleted a secret: %v", err)
		}
	}
	if err := catalog.Delete(t.Context(), initial.Metadata.Key, initial.Metadata.ID, 2); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("wrong version deleted a secret: %v", err)
	}
	if err := catalog.Delete(t.Context(), initial.Metadata.Key, initial.Metadata.ID, 1); err != nil {
		t.Fatal(err)
	}
	catalog = openSecretMongoStore(t, database)
	if err := catalog.Delete(t.Context(), initial.Metadata.Key, initial.Metadata.ID, 1); err != nil {
		t.Fatalf("deletion replay: %v", err)
	}
	if _, err := catalog.Get(t.Context(), initial.Metadata.Key); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("deleted record still visible: %v", err)
	}
	if err := catalog.Create(t.Context(), initial); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("delayed create restored a deleted identity: %v", err)
	}
	absent := initial.Metadata.Key
	absent.Name = "ABSENT"
	for _, wrongID := range []string{initial.Metadata.ID, other.Metadata.ID} {
		if err := catalog.Delete(t.Context(), absent, wrongID, 1); !errors.Is(err, secrets.ErrSecretConflict) {
			t.Fatalf("absent-key deletion reused another key's ID: %v", err)
		}
	}
	fresh := mongoSecretFixture(t, initial.Metadata.Key.Name)
	if err := catalog.Create(t.Context(), fresh); err != nil {
		t.Fatalf("fresh ID could not recreate a key: %v", err)
	}
	if err := catalog.Delete(t.Context(), initial.Metadata.Key, initial.Metadata.ID, 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("stale delete affected a recreated key: %v", err)
	}
	old := initial.Metadata
	old.Version++
	old.UpdatedAt = old.UpdatedAt.Add(time.Nanosecond)
	if err := catalog.Replace(t.Context(), encryptMongoSecret(t, old), 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("stale replacement affected a recreated key: %v", err)
	}
	delayed := mongoSecretFixture(t, "DELAYED_TOKEN")
	if err := catalog.Delete(t.Context(), delayed.Metadata.Key, delayed.Metadata.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := openSecretMongoStore(t, database).Create(t.Context(), delayed); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("late initial create restored a retired identity: %v", err)
	}
	assertMongoSecret(t, catalog, fresh)
	assertMongoSecret(t, catalog, other)
}

func TestSecretMongoStoreInvalidContexts(t *testing.T) {
	database := openStore(t, "secrets")
	catalog := openSecretMongoStore(t, database)
	initial, fresh := mongoSecretFixture(t, "API_TOKEN"), mongoSecretFixture(t, "NEW_TOKEN")
	if err := catalog.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	metadata := initial.Metadata
	metadata.Version++
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
	replacement := encryptMongoSecret(t, metadata)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, cancelled} {
		checks := []func() error{
			func() error { _, err := secretmongo.NewSecretMongoStore(ctx, database); return err },
			func() error { return catalog.Create(ctx, fresh) },
			func() error { return catalog.Replace(ctx, replacement, 1) },
			func() error { return catalog.Delete(ctx, initial.Metadata.Key, initial.Metadata.ID, 1) },
			func() error { _, err := catalog.Get(ctx, initial.Metadata.Key); return err },
			func() error { _, err := catalog.GetMetadata(ctx, initial.Metadata.Key); return err },
			func() error { _, err := catalog.List(ctx, initial.Metadata.Key.Scope); return err },
		}
		for i, check := range checks {
			if err := check(); err == nil || (ctx != nil && !errors.Is(err, context.Canceled)) {
				t.Errorf("operation %d accepted invalid context: %v", i, err)
			}
		}
	}
	assertMongoSecret(t, catalog, initial)
	if _, err := catalog.Get(t.Context(), fresh.Metadata.Key); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("rejected create changed persisted state: %v", err)
	}
}

func TestSecretMongoStoreConcurrentInstances(t *testing.T) {
	database := openStore(t, "secrets")
	stores := []secrets.SecretStore{openSecretMongoStore(t, database), openSecretMongoStore(t, database)}
	const workers = 8
	creates := make([]secrets.Secret, workers)
	for i := range creates {
		creates[i] = mongoSecretFixture(t, "API_TOKEN")
	}
	compete := func(candidates []secrets.Secret, write func(context.Context, secrets.SecretStore, secrets.Secret) error) secrets.Secret {
		t.Helper()
		results := concurrentMongoSecretWrites(t, workers, func(ctx context.Context, i int) error {
			return write(ctx, stores[i%len(stores)], candidates[i])
		})
		winner, successes := 0, 0
		for i, err := range results {
			if err == nil {
				winner, successes = i, successes+1
			} else if !errors.Is(err, secrets.ErrSecretConflict) {
				t.Errorf("competing write: %v", err)
			}
		}
		if successes != 1 {
			t.Fatalf("competing writes had %d winners", successes)
		}
		return candidates[winner]
	}
	winner := compete(creates, func(ctx context.Context, catalog secrets.SecretStore, value secrets.Secret) error {
		return catalog.Create(ctx, value)
	})
	assertMongoSecret(t, stores[0], winner)
	replacements := make([]secrets.Secret, workers)
	for i := range replacements {
		metadata := winner.Metadata
		metadata.Version++
		metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Duration(i+1) * time.Nanosecond)
		replacements[i] = encryptMongoSecret(t, metadata)
	}
	latest := compete(replacements, func(ctx context.Context, catalog secrets.SecretStore, value secrets.Secret) error {
		return catalog.Replace(ctx, value, 1)
	})
	assertMongoSecret(t, openSecretMongoStore(t, database), latest)
}

func TestSecretMongoStoreConcurrentIdenticalReplays(t *testing.T) {
	database := openStore(t, "secrets")
	stores := []secrets.SecretStore{openSecretMongoStore(t, database), openSecretMongoStore(t, database)}
	initial, absent := mongoSecretFixture(t, "API_TOKEN"), mongoSecretFixture(t, "DELAYED_TOKEN")
	for _, write := range []func(context.Context, int) error{
		func(ctx context.Context, i int) error { return stores[i%len(stores)].Create(ctx, initial) },
		func(ctx context.Context, i int) error {
			return stores[i%len(stores)].Delete(ctx, absent.Metadata.Key, absent.Metadata.ID, 1)
		},
	} {
		for _, err := range concurrentMongoSecretWrites(t, 8, write) {
			if err != nil {
				t.Errorf("concurrent identical replay failed: %v", err)
			}
		}
	}
	assertMongoSecret(t, stores[0], initial)
	if err := stores[0].Create(t.Context(), absent); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("concurrent absent deletions failed to retire identity: %v", err)
	}
}

func TestSecretMongoStoreRejectsCorruptPayloads(t *testing.T) {
	for _, test := range []struct {
		name string
		data func(secrets.Secret) []byte
	}{
		{"malformed JSON", func(secrets.Secret) []byte { return []byte("{") }},
		{"wrong ID", func(s secrets.Secret) []byte {
			s.Metadata.ID = id.New()
			data, _ := json.Marshal(s)
			return data
		}},
		{"wrong indexed key", func(s secrets.Secret) []byte {
			s.Metadata.Key.Name = "OTHER_TOKEN"
			data, _ := json.Marshal(s)
			return data
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			database := openStore(t, "secrets")
			catalog := openSecretMongoStore(t, database)
			initial := mongoSecretFixture(t, "API_TOKEN")
			if err := catalog.Create(t.Context(), initial); err != nil {
				t.Fatal(err)
			}
			payload := test.data(initial)
			if _, err := database.Update(t.Context(), "secrets", bson.M{"_id": initial.Metadata.ID}, bson.M{"$set": bson.M{"data": payload}}); err != nil {
				t.Fatal(err)
			}
			metadata := initial.Metadata
			metadata.Version++
			metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
			replacement := encryptMongoSecret(t, metadata)
			for i, check := range []func() error{
				func() error { _, err := catalog.Get(t.Context(), initial.Metadata.Key); return err },
				func() error { _, err := catalog.GetMetadata(t.Context(), initial.Metadata.Key); return err },
				func() error { _, err := catalog.List(t.Context(), initial.Metadata.Key.Scope); return err },
				func() error { return catalog.Create(t.Context(), initial) },
				func() error { return catalog.Replace(t.Context(), replacement, 1) },
				func() error { return catalog.Delete(t.Context(), initial.Metadata.Key, initial.Metadata.ID, 1) },
			} {
				if err := check(); err == nil {
					t.Errorf("operation %d accepted corrupt persisted data", i)
				}
			}
			var saved struct{ Data []byte }
			if err := database.GetByID(t.Context(), "secrets", initial.Metadata.ID, &saved); err != nil || !bytes.Equal(saved.Data, payload) {
				t.Fatalf("rejected write changed corrupt record: %v", err)
			}
		})
	}
}

func TestSecretMongoStorePreservesUnsignedVersions(t *testing.T) {
	database := openStore(t, "secrets")
	catalog := openSecretMongoStore(t, database)
	initial := mongoSecretFixture(t, "API_TOKEN")
	if err := catalog.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	// Seed a valid high version directly instead of performing 2^64 updates.
	metadata := initial.Metadata
	metadata.Version = math.MaxUint64 - 1
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
	previous := encryptMongoSecret(t, metadata)
	data, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Update(t.Context(), "secrets", bson.M{"_id": metadata.ID}, bson.M{"$set": bson.M{"data": data}}); err != nil {
		t.Fatal(err)
	}
	metadata.Version++
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
	latest := encryptMongoSecret(t, metadata)
	if err := catalog.Replace(t.Context(), latest, math.MaxUint64-1); err != nil {
		t.Fatalf("largest unsigned version rejected: %v", err)
	}
	assertMongoSecret(t, openSecretMongoStore(t, database), latest)
	if err := catalog.Replace(t.Context(), latest, math.MaxUint64); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("overflow accepted: %v", err)
	}
	assertMongoSecret(t, catalog, latest)
	if err := catalog.Delete(t.Context(), metadata.Key, metadata.ID, math.MaxUint64); err != nil {
		t.Fatalf("largest unsigned version could not be deleted: %v", err)
	}
}

func concurrentMongoSecretWrites(t *testing.T, count int, write func(context.Context, int) error) []error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	gate, done := make(chan struct{}), make(chan struct{})
	results := make([]error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			results[i] = write(ctx, i)
		}()
	}
	go func() { wg.Wait(); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("concurrent secret writes did not stop after cancellation")
		}
	}()
	close(gate)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed out awaiting concurrent secret writes")
	}
	return results
}

func openSecretMongoStore(t *testing.T, database *store.MongoStore) *secretmongo.SecretMongoStore {
	t.Helper()
	catalog, err := secretmongo.NewSecretMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func mongoSecretFixture(t *testing.T, name string) secrets.Secret {
	t.Helper()
	stamp := time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC)
	return encryptMongoSecret(t, secrets.Metadata{
		ID: id.New(), Key: secrets.Key{Scope: secrets.Scope{AgentType: "builder-agent", Profile: secrets.DefaultProfile}, Name: name},
		Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
	})
}

func encryptMongoSecret(t *testing.T, metadata secrets.Metadata) secrets.Secret {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(mongoSecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	value, err := cipher.Encrypt(metadata, []byte(mongoSecretPlaintext))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mongoSecretOtherZone(value secrets.Secret) secrets.Secret {
	replay := value.Clone()
	zone := time.FixedZone("other representation", 3*60*60)
	replay.Metadata.CreatedAt = replay.Metadata.CreatedAt.In(zone)
	replay.Metadata.UpdatedAt = replay.Metadata.UpdatedAt.In(zone)
	if !replay.Metadata.ExpiresAt.IsZero() {
		replay.Metadata.ExpiresAt = replay.Metadata.ExpiresAt.In(zone)
	}
	return replay
}

func assertMongoSecret(t *testing.T, catalog secrets.SecretStore, expected secrets.Secret) {
	t.Helper()
	got, err := catalog.Get(t.Context(), expected.Metadata.Key)
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(metadata secrets.Metadata) secrets.Metadata {
		metadata.CreatedAt, metadata.UpdatedAt, metadata.ExpiresAt = metadata.CreatedAt.UTC(), metadata.UpdatedAt.UTC(), metadata.ExpiresAt.UTC()
		return metadata
	}
	if canonical(got.Metadata) != canonical(expected.Metadata) || !reflect.DeepEqual(got.Value, expected.Value) {
		t.Fatal("saved secret differs from expected record")
	}
	cipher, err := crypto.NewSecretCipher(mongoSecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(got)
	if err != nil || !bytes.Equal(plaintext, []byte(mongoSecretPlaintext)) {
		t.Fatalf("saved ciphertext could not be authenticated: %v", err)
	}
}
