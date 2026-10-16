package providers_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretfs "github.com/defos-ai/commander/providers/fs/secrets"
)

func TestSecretFsStoreRejectsCorruptSnapshots(t *testing.T) {
	value := fsSecretFixture(t, "API_TOKEN")
	encode := func(records ...map[string]any) []byte {
		t.Helper()
		data, err := json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	duplicateID, duplicateKey := value.Clone(), value.Clone()
	duplicateID.Metadata.Key.Name = "OTHER_TOKEN"
	duplicateKey.Metadata.ID = id.New()
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"invalid JSON", []byte(`[{`)},
		{"empty entry", encode(map[string]any{})},
		{"both alternatives", encode(map[string]any{"secret": value, "retired": map[string]any{"id": value.Metadata.ID, "key": value.Metadata.Key}})},
		{"duplicate ID", encode(map[string]any{"secret": value}, map[string]any{"secret": duplicateID})},
		{"duplicate active key", encode(map[string]any{"secret": value}, map[string]any{"secret": duplicateKey})},
		{"unknown field", encode(map[string]any{"secret": value, "unrecognized": true})},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store := openSecretFsStore(t, root)
			path := filepath.Join(root, "secrets.json")
			if err := os.WriteFile(path, test.data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := secretfs.NewSecretFsStore(root); err == nil {
				t.Fatal("constructor accepted corrupt state")
			}
			metadata := value.Metadata
			metadata.Version++
			replacement := encryptFsSecret(t, metadata)
			checks := []func() error{
				func() error { _, err := store.Get(t.Context(), value.Metadata.Key); return err },
				func() error { _, err := store.List(t.Context(), value.Metadata.Key.Scope); return err },
				func() error { return store.Create(t.Context(), value) },
				func() error { return store.Replace(t.Context(), replacement, 1) },
				func() error { return store.Delete(t.Context(), value.Metadata.Key, value.Metadata.ID, 1) },
			}
			for i, check := range checks {
				if err := check(); err == nil {
					t.Errorf("operation %d accepted corrupt state", i)
				}
				assertSecretFileUnchanged(t, path, test.data)
			}
		})
	}
}

func TestSecretFsStoreRejectsCollectionAndLockSymlinks(t *testing.T) {
	for _, name := range []string{"secrets.json", "secrets.json.lock"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			store := openSecretFsStore(t, root)
			value := fsSecretFixture(t, "API_TOKEN")
			if err := store.Create(t.Context(), value); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "outside.json")
			sentinel := []byte("[]\n")
			if err := os.WriteFile(outside, sentinel, 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, name)
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			linkForTest(t, outside, link)
			if _, err := secretfs.NewSecretFsStore(root); err == nil {
				t.Fatal("constructor followed symlink")
			}
			if _, err := store.Get(t.Context(), value.Metadata.Key); err == nil {
				t.Fatal("read followed symlink")
			}
			if err := store.Create(t.Context(), value); err == nil {
				t.Fatal("write followed symlink")
			}
			assertSecretFileUnchanged(t, outside, sentinel)
		})
	}
}

func TestSecretFsStoreRejectsInvalidRecords(t *testing.T) {
	store := openSecretFsStore(t, t.TempDir())
	value := fsSecretFixture(t, "API_TOKEN")
	if err := store.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*secrets.Secret)
	}{
		{"zero version", func(s *secrets.Secret) { s.Metadata.Version = 0 }},
		{"invalid key", func(s *secrets.Secret) { s.Metadata.Key.Name = "../outside" }},
		{"missing nonce", func(s *secrets.Secret) { s.Value.Nonce = nil }},
		{"missing ciphertext", func(s *secrets.Secret) { s.Value.Ciphertext = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := value.Clone()
			invalid.Metadata.ID = id.New()
			invalid.Metadata.Key.Name = "NEW_TOKEN"
			test.edit(&invalid)
			if err := store.Create(t.Context(), invalid); err == nil {
				t.Fatal("create accepted malformed record")
			}
			invalid = value.Clone()
			invalid.Metadata.Version++
			test.edit(&invalid)
			if err := store.Replace(t.Context(), invalid, 1); err == nil {
				t.Fatal("replace accepted malformed record")
			}
		})
	}
	for _, edit := range []func(*secrets.Metadata){
		func(m *secrets.Metadata) { m.Version = 2 },
		func(m *secrets.Metadata) { m.UpdatedAt = m.UpdatedAt.Add(time.Nanosecond) },
	} {
		metadata := fsSecretFixture(t, "NEW_TOKEN").Metadata
		edit(&metadata)
		if err := store.Create(t.Context(), encryptFsSecret(t, metadata)); err == nil {
			t.Fatal("create accepted a noninitial version or update time")
		}
	}
	assertFsSecret(t, store, value)
	items, err := store.List(t.Context(), value.Metadata.Key.Scope)
	if err != nil || len(items) != 1 {
		t.Fatalf("invalid writes changed inventory: count=%d, error=%v", len(items), err)
	}
}

func TestSecretFsStoreRetiredIdentityStaysBoundToKey(t *testing.T) {
	root := t.TempDir()
	store := openSecretFsStore(t, root)
	value := fsSecretFixture(t, "API_TOKEN")
	if err := store.Delete(t.Context(), value.Metadata.Key, value.Metadata.ID, 1); err != nil {
		t.Fatal(err)
	}
	other := value.Metadata.Key
	other.Name = "OTHER_TOKEN"
	reopened := openSecretFsStore(t, root)
	if err := reopened.Delete(t.Context(), other, value.Metadata.ID, 1); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("deletion reassigned a retired ID to another key: %v", err)
	}
	if err := reopened.Create(t.Context(), value); !errors.Is(err, secrets.ErrSecretConflict) {
		t.Fatalf("failed deletion lost identity retirement: %v", err)
	}
}

func assertSecretFileUnchanged(t *testing.T, path string, expected []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, expected) {
		t.Fatalf("rejected operation changed persisted bytes: %v", err)
	}
}
