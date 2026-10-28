package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretmongo "github.com/defos-ai/commander/providers/mongo/secrets"
	"github.com/defos-ai/commander/providers/mongo/store"
)

const SecretEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

const SecretPlaintext = "private-test-token-never-store-as-plaintext"

func OpenSecretStore(t *testing.T, database *store.MongoStore) *secretmongo.SecretMongoStore {
	t.Helper()
	catalog, err := secretmongo.NewSecretMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func Secret(t *testing.T, name string) secrets.Secret {
	t.Helper()
	stamp := time.Date(2026, 10, 16, 12, 0, 0, 123456789, time.UTC)
	return EncryptSecret(t, secrets.Metadata{
		ID: id.New(), Key: secrets.Key{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: name},
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

func SecretOtherZone(value secrets.Secret) secrets.Secret {
	replay := value.Clone()
	zone := time.FixedZone("other representation", 3*60*60)
	replay.Metadata.CreatedAt = replay.Metadata.CreatedAt.In(zone)
	replay.Metadata.UpdatedAt = replay.Metadata.UpdatedAt.In(zone)
	if !replay.Metadata.ExpiresAt.IsZero() {
		replay.Metadata.ExpiresAt = replay.Metadata.ExpiresAt.In(zone)
	}
	return replay
}
