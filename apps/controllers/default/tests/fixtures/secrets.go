package fixtures

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fsagents "github.com/defos-ai/commander/providers/fs/agents"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

const SecretEncryptionKey = "014ac583269fe047014ac583269fe047014ac583269fe047014ac583269fe047"

func SecretCipher(t *testing.T) *crypto.SecretCipher {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(SecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func PendingSecret(t *testing.T, root string) {
	t.Helper()
	now := time.Now().UTC()
	request := secrets.Request{
		ID: id.New(), AgentID: "fixture-agent-01234567", AgentType: "fixture", Key: secrets.Key{Name: "API_TOKEN"},
		Status: secrets.Pending, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	secret, err := SecretCipher(t).Encrypt(secrets.Metadata{
		ID: id.New(), Key: request.Key, Version: 1, CreatedAt: now, UpdatedAt: now,
	}, []byte("local-only-fixture-token"))
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(root, "database")
	values, err := fssecrets.NewSecretFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := values.Create(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	inventory, err := fsagents.NewAgentFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := inventory.Upsert(t.Context(), agents.Agent{
		AgentSnapshot: agents.AgentSnapshot{ID: request.AgentID, Type: "fixture", Status: agents.AgentReady},
		ObservedAt:    now, LastSeen: now, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	requests, err := fssecrets.NewRequestFsStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
}
