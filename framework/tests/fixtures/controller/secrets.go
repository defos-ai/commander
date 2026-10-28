package fixtures

import (
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
)

func SecretConsumer(t *testing.T) (consumers.SecretEventConsumer, *fakes.SecretRequests, *fakes.SecretValues, *fakes.SecretPublishBus) {
	t.Helper()
	now := time.Now().UTC()
	snapshot := ObservationSnapshot()
	snapshot.Actions[0].RequiredSecrets = []string{"API_TOKEN"}
	inventory := &fakes.Inventory{}
	inventory.Set(&agents.Agent{AgentSnapshot: snapshot, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Minute)})
	requests := fakes.NewSecretRequests()
	values := &fakes.SecretValues{}
	values.Set(&secrets.Secret{
		Metadata: secrets.Metadata{ID: id.MessageID(), Key: SecretSyncRequest().Key, Version: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
		Value:    secrets.EncryptedValue{Format: "test-envelope", KeyID: "test-key", Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext")},
	})
	bus := &fakes.SecretPublishBus{}
	return consumers.SecretEventConsumer{Bus: bus, Inventory: inventory, Secrets: values, Requests: requests}, requests, values, bus
}

func SecretSyncRequest() secrets.Request {
	now := time.Now().UTC()
	return secrets.Request{
		ID: id.MessageID(), AgentID: AgentID, Key: secrets.Key{Scope: secrets.Scope{AgentType: "fixture"}, Name: "API_TOKEN"},
		Status: secrets.Pending, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}

func SecretRequestMessage(t *testing.T, request secrets.Request) messages.Message {
	t.Helper()
	message, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
