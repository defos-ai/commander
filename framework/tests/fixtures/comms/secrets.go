package fixtures

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

const SecretValue = "private-contract-test-value"
const EncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func SecretProtocol(t *testing.T) (secretmodel.Request, secretmodel.Secret) {
	t.Helper()
	now := time.Now().UTC()
	key := secretmodel.Key{Name: "API_TOKEN"}
	request := secretmodel.Request{
		ID: id.New(), Key: key, AgentID: "builder-agent-12345678", AgentType: "builder-agent", Status: secretmodel.Pending,
		RequestedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}
	metadata := secretmodel.Metadata{
		ID: id.New(), Key: key, Version: 1,
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(30 * time.Minute),
	}
	cipher, err := crypto.NewSecretCipher(EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := cipher.Encrypt(metadata, []byte(SecretValue))
	if err != nil {
		t.Fatal("could not prepare encrypted protocol fixture")
	}
	return request, secret
}

func SecretWirePayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
