package fixtures

import (
	"math"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretmongo "github.com/defos-ai/commander/providers/mongo/secrets"
	"github.com/defos-ai/commander/providers/mongo/store"
)

func OpenRequestStore(t *testing.T, database *store.MongoStore) *secretmongo.RequestMongoStore {
	t.Helper()
	requests, err := secretmongo.NewRequestMongoStore(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	return requests
}

func SecretRequest(t *testing.T) (secrets.Request, secrets.Delivery, secrets.Installation) {
	t.Helper()
	secret := Secret(t, "API_TOKEN")
	secret.Metadata.Version = math.MaxUint64
	stamp := secret.Metadata.UpdatedAt.Add(time.Second)
	secret.Metadata.ExpiresAt = stamp.Add(time.Minute)
	secret = EncryptSecret(t, secret.Metadata)
	request := secrets.Request{ID: id.New(), Key: secret.Metadata.Key, AgentID: "builder-agent-12345678",
		Status: secrets.Pending, RequestedAt: stamp, ExpiresAt: stamp.Add(time.Hour)}
	delivery := secrets.Delivery{ID: id.New(), RequestID: request.ID, AgentID: request.AgentID, Secret: secret,
		CreatedAt: stamp.Add(time.Nanosecond), ExpiresAt: stamp.Add(time.Minute)}
	installation := secrets.Installation{ID: id.New(), RequestID: request.ID, DeliveryID: delivery.ID, AgentID: request.AgentID,
		SecretID: secret.Metadata.ID, SecretVersion: secret.Metadata.Version, InstalledAt: stamp.Add(time.Second)}
	return request, delivery, installation
}
