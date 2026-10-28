package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

const DeliveryID = "00000000-0000-4000-8000-000000000003"
const InstallationID = "00000000-0000-4000-8000-000000000006"
const EncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func RequestedAt() time.Time { return time.Date(2026, time.October, 16, 12, 0, 0, 0, time.UTC) }
func Secret() secrets.Secret {
	return secrets.Secret{
		Metadata: secrets.Metadata{
			ID: "00000000-0000-4000-8000-000000000001",
			Key: secrets.Key{
				Scope: secrets.Scope{AgentType: "browser"},
				Name:  "API_TOKEN",
			},
			Version: 2, CreatedAt: RequestedAt().Add(-time.Hour), UpdatedAt: RequestedAt(),
			ExpiresAt: RequestedAt().Add(time.Hour),
		},
		Value: secrets.EncryptedValue{
			Format: "test-envelope", KeyID: "test-key",
			Nonce: []byte("nonce-fixture"), Ciphertext: []byte("private-payload-fixture"),
		},
	}
}

func Request() secrets.Request {
	return secrets.Request{
		ID: "00000000-0000-4000-8000-000000000002", Key: Secret().Metadata.Key,
		AgentID: "builder-agent-01234567", Status: secrets.Pending,
		RequestedAt: RequestedAt(), ExpiresAt: RequestedAt().Add(15 * time.Minute),
	}
}

func Cipher(t *testing.T) *crypto.SecretCipher {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func PendingRecord(t *testing.T) secrets.RequestRecord {
	t.Helper()
	record, err := secrets.NewRequestRecord(Request())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func Delivery(t *testing.T) secrets.Delivery {
	t.Helper()
	secret, err := Cipher(t).Encrypt(Secret().Metadata, []byte("record-secret-plaintext-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	request := Request()
	return secrets.Delivery{ID: DeliveryID, RequestID: request.ID, AgentID: request.AgentID,
		Secret: secret, CreatedAt: RequestedAt().Add(time.Minute), ExpiresAt: request.ExpiresAt}
}

func DeliveryRecord(t *testing.T) secrets.RequestRecord {
	t.Helper()
	record, err := PendingRecord(t).WithDelivery(Delivery(t))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func Installation(delivery secrets.Delivery) secrets.Installation {
	return secrets.Installation{ID: InstallationID, RequestID: delivery.RequestID,
		DeliveryID: delivery.ID, AgentID: delivery.AgentID, SecretID: delivery.Secret.Metadata.ID,
		SecretVersion: delivery.Secret.Metadata.Version, InstalledAt: delivery.CreatedAt.Add(time.Minute)}
}
