package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretfs "github.com/defos-ai/commander/providers/fs/secrets"
)

const SecretEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const SecretPlaintext = "private-test-token-never-store-as-plaintext"

func OpenSecretStore(t *testing.T, root string) *secretfs.SecretFsStore {
	t.Helper()
	store, err := secretfs.NewSecretFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func Secret(t *testing.T, name string) secrets.Secret {
	t.Helper()
	stamp := time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC)
	return EncryptSecret(t, secrets.Metadata{
		ID: id.New(), Key: secrets.Key{Name: name},
		Version: 1, CreatedAt: stamp, UpdatedAt: stamp,
	})
}

func EncryptSecret(t *testing.T, metadata secrets.Metadata) secrets.Secret {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(SecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	value, err := cipher.Encrypt(metadata, []byte(SecretPlaintext))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
