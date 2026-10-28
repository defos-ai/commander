package fixtures

import (
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretfs "github.com/defos-ai/commander/providers/fs/secrets"
)

func OpenRequestStore(t *testing.T, root string) *secretfs.RequestFsStore {
	t.Helper()
	store, err := secretfs.NewRequestFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func SecretRequest(t *testing.T) (secrets.Request, secrets.Delivery, secrets.Installation) {
	t.Helper()
	secret := Secret(t, "API_TOKEN")
	request := secrets.Request{
		ID: id.New(), Key: secret.Metadata.Key, AgentID: "builder-agent-12345678", Status: secrets.Pending,
		RequestedAt: secret.Metadata.CreatedAt, ExpiresAt: secret.Metadata.CreatedAt.Add(time.Hour),
	}
	delivery := secrets.Delivery{
		ID: id.New(), RequestID: request.ID, AgentID: request.AgentID, Secret: secret,
		CreatedAt: request.RequestedAt.Add(time.Second), ExpiresAt: request.ExpiresAt,
	}
	return request, delivery, Installation(delivery)
}

func Installation(delivery secrets.Delivery) secrets.Installation {
	return secrets.Installation{
		ID: id.New(), RequestID: delivery.RequestID, DeliveryID: delivery.ID, AgentID: delivery.AgentID,
		SecretID: delivery.Secret.Metadata.ID, SecretVersion: delivery.Secret.Metadata.Version,
		InstalledAt: delivery.CreatedAt.Add(time.Second),
	}
}
