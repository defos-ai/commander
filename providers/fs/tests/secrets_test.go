package providers_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretfs "github.com/defos-ai/commander/providers/fs/secrets"
)

const fsSecretEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const fsSecretPlaintext = "private-test-token-never-store-as-plaintext"

func TestSecretFsStoreEncryptedRoundTripAndIsolation(t *testing.T) {
	root := t.TempDir()
	store := openSecretFsStore(t, root)
	value := fsSecretFixture(t, "API_TOKEN")
	expected := value.Clone()
	if err := store.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	value.Value.Nonce[0] ^= 1
	value.Value.Ciphertext[0] ^= 1
	reopened := openSecretFsStore(t, root)
	assertFsSecret(t, reopened, expected)
	got, err := reopened.Get(t.Context(), expected.Metadata.Key)
	if err != nil {
		t.Fatal(err)
	}
	got.Value.Nonce[0] ^= 1
	got.Value.Ciphertext[0] ^= 1
	assertFsSecret(t, reopened, expected)

	// Check all persisted files, including lock/staging files, without printing
	// their contents if a future implementation accidentally saves plaintext.
	keyBytes, _ := hex.DecodeString(fsSecretEncryptionKey)
	files := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		for _, sensitive := range [][]byte{[]byte(fsSecretPlaintext), []byte(fsSecretEncryptionKey), keyBytes} {
			if bytes.Contains(data, sensitive) {
				return errors.New("persisted data exposed plaintext or encryption key material")
			}
		}
		return nil
	})
	if err != nil || files == 0 {
		t.Fatalf("encrypted persistence check: files=%d, error=%v", files, err)
	}
}

func TestSecretFsStoreInventoryScopesAndExpiration(t *testing.T) {
	store := openSecretFsStore(t, t.TempDir())
	first, last := fsSecretFixture(t, "A_TOKEN"), fsSecretFixture(t, "Z_TOKEN")
	first.Metadata.ExpiresAt = first.Metadata.UpdatedAt.Add(time.Hour)
	first = encryptFsSecret(t, first.Metadata)
	agent := fsSecretFixture(t, "A_TOKEN")
	agent.Metadata.Key.Scope.AgentType = "other-agent"
	agent = encryptFsSecret(t, agent.Metadata)
	for _, value := range []secrets.Secret{last, first, agent} {
		if err := store.Create(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.List(t.Context(), first.Metadata.Key.Scope)
	if err != nil || !reflect.DeepEqual(items, []secrets.Metadata{first.Metadata, last.Metadata}) {
		t.Fatalf("inventory must be sorted and scoped: count=%d, error=%v", len(items), err)
	}
	if secrets.AvailabilityAt(&items[0], first.Metadata.ExpiresAt) != secrets.Expired {
		t.Fatal("inventory lost expired secret")
	}
	items[0].Key.Name = "changed-by-reader"
	metadata, err := store.GetMetadata(t.Context(), first.Metadata.Key)
	if err != nil || metadata != first.Metadata {
		t.Fatalf("inventory reader changed stored metadata: %v", err)
	}
	assertFsSecret(t, store, first)
	assertFsSecret(t, store, agent)
	otherItems, err := store.List(t.Context(), agent.Metadata.Key.Scope)
	if err != nil || !reflect.DeepEqual(otherItems, []secrets.Metadata{agent.Metadata}) {
		t.Fatalf("same secret name crossed agent types: count=%d, error=%v", len(otherItems), err)
	}
	unknown := first.Metadata.Key
	unknown.Name = "UNKNOWN"
	if _, err := store.Get(t.Context(), unknown); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("missing record: %v", err)
	}
	if _, err := store.GetMetadata(t.Context(), unknown); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("missing metadata: %v", err)
	}
}

func TestSecretFsStoreCreateIdentityAndReplay(t *testing.T) {
	store := openSecretFsStore(t, t.TempDir())
	value := fsSecretFixture(t, "API_TOKEN")
	if err := store.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	replay := value.Clone()
	zone := time.FixedZone("same instant", 3*60*60)
	replay.Metadata.CreatedAt = replay.Metadata.CreatedAt.In(zone)
	replay.Metadata.UpdatedAt = replay.Metadata.UpdatedAt.In(zone)
	if err := store.Create(t.Context(), replay); err != nil {
		t.Fatalf("equal instants must count as an identical replay: %v", err)
	}
	for _, change := range []func(*secrets.Secret){
		func(s *secrets.Secret) { s.Metadata.ID = id.New() },
		func(s *secrets.Secret) { s.Metadata.Key.Name = "OTHER_TOKEN" },
		func(s *secrets.Secret) { s.Value.Ciphertext[0] ^= 1 },
	} {
		conflict := value.Clone()
		change(&conflict)
		if err := store.Create(t.Context(), conflict); !errors.Is(err, secrets.ErrSecretConflict) {
			t.Fatalf("conflicting create: %v", err)
		}
	}
	assertFsSecret(t, store, value)
}

func TestSecretFsStoreReplaceCASAndReplay(t *testing.T) {
	root := t.TempDir()
	store, other := openSecretFsStore(t, root), openSecretFsStore(t, root)
	initial := fsSecretFixture(t, "API_TOKEN")
	if err := store.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	metadata := initial.Metadata
	metadata.Version++
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
	replacement := encryptFsSecret(t, metadata)
	if err := other.Replace(t.Context(), replacement, 1); err != nil {
		t.Fatal(err)
	}
	replay := replacement.Clone()
	zone := time.FixedZone("other instant representation", -4*60*60)
	replay.Metadata.CreatedAt = replay.Metadata.CreatedAt.In(zone)
	replay.Metadata.UpdatedAt = replay.Metadata.UpdatedAt.In(zone)
	if err := store.Replace(t.Context(), replay, 1); err != nil {
		t.Fatalf("replacement replay: %v", err)
	}
	conflict := replacement.Clone()
	conflict.Value.Ciphertext[0] ^= 1
	if err := store.Replace(t.Context(), conflict, 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("competing replacement accepted: %v", err)
	}
	metadata.Version++
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Nanosecond)
	latest := encryptFsSecret(t, metadata)
	if err := store.Replace(t.Context(), latest, 2); err != nil {
		t.Fatal(err)
	}
	if err := other.Replace(t.Context(), replacement, 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("stale replay overwrote a newer version: %v", err)
	}
	assertFsSecret(t, openSecretFsStore(t, root), latest)
}

func TestSecretFsStoreReplacePreservesIdentityAndVersion(t *testing.T) {
	store := openSecretFsStore(t, t.TempDir())
	initial := fsSecretFixture(t, "API_TOKEN")
	if err := store.Create(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	metadata := initial.Metadata
	metadata.Version = 2
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Hour)
	saved := encryptFsSecret(t, metadata)
	if err := store.Replace(t.Context(), saved, 1); err != nil {
		t.Fatal(err)
	}
	other := fsSecretFixture(t, "OTHER_TOKEN")
	if err := store.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	metadata.Version = 3
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Hour)
	for _, test := range []struct {
		name string
		edit func(*secrets.Metadata)
	}{
		{"changed ID", func(m *secrets.Metadata) { m.ID = id.New() }},
		{"changed key", func(m *secrets.Metadata) { m.Key = other.Metadata.Key }},
		{"changed creation", func(m *secrets.Metadata) { m.CreatedAt = m.CreatedAt.Add(-time.Second) }},
		{"backward update", func(m *secrets.Metadata) { m.UpdatedAt = saved.Metadata.UpdatedAt.Add(-time.Minute) }},
		{"skipped version", func(m *secrets.Metadata) { m.Version = 4 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := metadata
			test.edit(&changed)
			if err := store.Replace(t.Context(), encryptFsSecret(t, changed), 2); !errors.Is(err, secrets.ErrSecretConflict) {
				t.Fatalf("invalid replacement: %v", err)
			}
		})
	}
	for _, expectedVersion := range []uint64{0, math.MaxUint64} {
		if err := store.Replace(t.Context(), initial, expectedVersion); err == nil {
			t.Fatalf("invalid expected version %d accepted", expectedVersion)
		}
	}
	zero := initial.Clone()
	zero.Metadata.Version = 0
	if err := store.Replace(t.Context(), zero, math.MaxUint64); err == nil {
		t.Fatal("version overflow accepted")
	}
	missing := metadata
	missing.Key.Name = "MISSING_TOKEN"
	if err := store.Replace(t.Context(), encryptFsSecret(t, missing), 2); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("replacement created a new key: %v", err)
	}
	assertFsSecret(t, store, saved)
	assertFsSecret(t, store, other)
}

func TestSecretFsStoreDeletionRetiresIdentity(t *testing.T) {
	root := t.TempDir()
	store := openSecretFsStore(t, root)
	first := fsSecretFixture(t, "API_TOKEN")
	other := fsSecretFixture(t, "OTHER_TOKEN")
	for _, value := range []secrets.Secret{first, other} {
		if err := store.Create(t.Context(), value); err != nil {
			t.Fatal(err)
		}
	}
	for _, wrongID := range []string{id.New(), other.Metadata.ID} {
		if err := store.Delete(t.Context(), first.Metadata.Key, wrongID, 1); !errors.Is(err, secrets.ErrSecretConflict) {
			t.Fatalf("wrong ID deleted a secret: %v", err)
		}
	}
	if err := store.Delete(t.Context(), first.Metadata.Key, first.Metadata.ID, 2); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("wrong version deleted a secret: %v", err)
	}
	absent := first.Metadata.Key
	absent.Name = "ABSENT"
	if err := store.Delete(t.Context(), absent, other.Metadata.ID, 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("absent-key deletion retired another active secret: %v", err)
	}
	if err := store.Delete(t.Context(), first.Metadata.Key, first.Metadata.ID, 1); err != nil {
		t.Fatal(err)
	}
	store = openSecretFsStore(t, root)
	if err := store.Delete(t.Context(), first.Metadata.Key, first.Metadata.ID, 1); err != nil {
		t.Fatalf("repeated deletion: %v", err)
	}
	if _, err := store.Get(t.Context(), first.Metadata.Key); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("deleted secret remained visible: %v", err)
	}
	if err := store.Create(t.Context(), first); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("delayed create resurrected a deleted secret: %v", err)
	}
	relocated := first.Metadata
	relocated.Key.Name = "RELOCATED"
	if err := store.Create(t.Context(), encryptFsSecret(t, relocated)); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("retired ID reused under a different key: %v", err)
	}
	fresh := fsSecretFixture(t, first.Metadata.Key.Name)
	if err := store.Create(t.Context(), fresh); err != nil {
		t.Fatalf("fresh identity could not recreate key: %v", err)
	}
	if err := store.Delete(t.Context(), first.Metadata.Key, first.Metadata.ID, 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("stale delete affected recreated secret: %v", err)
	}
	assertFsSecret(t, store, fresh)
	assertFsSecret(t, store, other)

	delayed := fsSecretFixture(t, "DELAYED_TOKEN")
	if err := store.Delete(t.Context(), delayed.Metadata.Key, delayed.Metadata.ID, 1); err != nil {
		t.Fatalf("delete before delayed create: %v", err)
	}
	if err := openSecretFsStore(t, root).Create(t.Context(), delayed); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("delayed first create restored a retired identity: %v", err)
	}
}

func TestSecretFsStoreCancelledAndNilContexts(t *testing.T) {
	store := openSecretFsStore(t, t.TempDir())
	value := fsSecretFixture(t, "API_TOKEN")
	if err := store.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	metadata := value.Metadata
	metadata.Version++
	metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Second)
	replacement := encryptFsSecret(t, metadata)
	fresh := fsSecretFixture(t, "NEW_TOKEN")
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name string
		ctx  context.Context
	}{
		{"cancelled", cancelled}, {"nil", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			checks := []func() error{
				func() error { return store.Create(test.ctx, fresh) },
				func() error { return store.Replace(test.ctx, replacement, 1) },
				func() error { return store.Delete(test.ctx, value.Metadata.Key, value.Metadata.ID, 1) },
				func() error { _, err := store.Get(test.ctx, value.Metadata.Key); return err },
				func() error { _, err := store.GetMetadata(test.ctx, value.Metadata.Key); return err },
				func() error { _, err := store.List(test.ctx, value.Metadata.Key.Scope); return err },
			}
			for i, check := range checks {
				err := check()
				if err == nil || (test.ctx != nil && !errors.Is(err, context.Canceled)) {
					t.Errorf("operation %d accepted invalid context: %v", i, err)
				}
			}
		})
	}
	assertFsSecret(t, store, value)
	if _, err := store.Get(t.Context(), fresh.Metadata.Key); !errors.Is(err, secrets.ErrUnknownSecret) {
		t.Fatalf("rejected create changed records: %v", err)
	}
}

func TestSecretFsStoreConcurrentInstances(t *testing.T) {
	root := t.TempDir()
	const workers = 12
	stores := make([]secrets.SecretStore, workers)
	for i := range workers {
		stores[i] = openSecretFsStore(t, root)
	}
	base := fsSecretFixture(t, "API_TOKEN")
	compete := func(candidates []secrets.Secret, write func(secrets.SecretStore, secrets.Secret) error) secrets.Secret {
		t.Helper()
		type result struct {
			index int
			err   error
		}
		gate, results := make(chan struct{}), make(chan result, workers)
		for i := range workers {
			go func() {
				<-gate
				results <- result{i, write(stores[i], candidates[i])}
			}()
		}
		close(gate)
		winner, successes := 0, 0
		for range workers {
			got := <-results
			if got.err == nil {
				winner, successes = got.index, successes+1
			} else if !errors.Is(got.err, secrets.ErrSecretConflict) {
				t.Errorf("concurrent write failed unexpectedly: %v", got.err)
			}
		}
		if successes != 1 {
			t.Fatalf("concurrent conflicting writes had %d winners", successes)
		}
		return candidates[winner]
	}
	creates := make([]secrets.Secret, workers)
	for i := range workers {
		creates[i] = fsSecretFixture(t, base.Metadata.Key.Name)
	}
	winner := compete(creates, func(store secrets.SecretStore, value secrets.Secret) error {
		return store.Create(t.Context(), value)
	})
	assertFsSecret(t, stores[0], winner)
	replacements := make([]secrets.Secret, workers)
	for i := range workers {
		metadata := winner.Metadata
		metadata.Version++
		metadata.UpdatedAt = metadata.UpdatedAt.Add(time.Duration(i+1) * time.Nanosecond)
		replacements[i] = encryptFsSecret(t, metadata)
	}
	latest := compete(replacements, func(store secrets.SecretStore, value secrets.Secret) error {
		return store.Replace(t.Context(), value, 1)
	})
	assertFsSecret(t, openSecretFsStore(t, root), latest)
}

func openSecretFsStore(t *testing.T, root string) *secretfs.SecretFsStore {
	t.Helper()
	store, err := secretfs.NewSecretFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func fsSecretFixture(t *testing.T, name string) secrets.Secret {
	t.Helper()
	stamp := time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC)
	return encryptFsSecret(t, secrets.Metadata{
		ID: id.New(), Key: secrets.Key{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: name},
		Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
	})
}

func encryptFsSecret(t *testing.T, metadata secrets.Metadata) secrets.Secret {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(fsSecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	value, err := cipher.Encrypt(metadata, []byte(fsSecretPlaintext))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func assertFsSecret(t *testing.T, store secrets.SecretStore, expected secrets.Secret) {
	t.Helper()
	got, err := store.Get(t.Context(), expected.Metadata.Key)
	if err != nil {
		t.Fatal(err)
	}
	canonical := func(metadata secrets.Metadata) secrets.Metadata {
		metadata.CreatedAt = metadata.CreatedAt.UTC()
		metadata.UpdatedAt = metadata.UpdatedAt.UTC()
		metadata.ExpiresAt = metadata.ExpiresAt.UTC()
		return metadata
	}
	if canonical(got.Metadata) != canonical(expected.Metadata) || !reflect.DeepEqual(got.Value, expected.Value) {
		t.Fatal("saved secret differs from expected record")
	}
	cipher, err := crypto.NewSecretCipher(fsSecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(got)
	if err != nil || !bytes.Equal(plaintext, []byte(fsSecretPlaintext)) {
		t.Fatalf("saved ciphertext could not be authenticated and recovered: %v", err)
	}
}
