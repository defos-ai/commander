package controller_test

import (
	"bytes"

	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"

	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/controller"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestSecretDeliveryPersistsBeforePublishingAndReplaysExactEvidence(t *testing.T) {
	consumer, requests, values, bus := fixtures.SecretConsumer(t)
	request := fixtures.SecretSyncRequest()
	bus.BeforePublish(func(message messages.Message) {
		record, err := requests.Get(t.Context(), request.ID)
		if err != nil || record.Delivery == nil || record.Delivery.ID != message.ID {
			t.Fatalf("publication preceded durable reservation: %v", err)
		}
	})
	requestMessage := fixtures.SecretRequestMessage(t, request)
	if err := consumer.Consume(t.Context(), requestMessage); err != nil {
		t.Fatal(err)
	}
	first := bus.Messages()[0]
	if bus.Topics()[0] != messages.AgentEvents(request.AgentID) {
		t.Fatal("secret delivery used the action command channel")
	}
	payload, err := agentevents.DecodeSecretDelivery(first)
	if err != nil || !reflect.DeepEqual(payload.Metadata, values.Snapshot().Metadata) || !bytes.Equal(payload.Value, []byte("plaintext-secret")) {
		t.Fatal("delivery did not carry the requested metadata and value")
	}
	reserved, err := requests.Get(t.Context(), request.ID)
	if err != nil || reserved.Delivery == nil || !reflect.DeepEqual(reserved.Delivery.Secret, *values.Snapshot()) {
		t.Fatal("delivery did not retain its encrypted snapshot in storage")
	}
	if err := consumer.Consume(t.Context(), requestMessage); err != nil {
		t.Fatal(err)
	}
	if len(bus.Messages()) != 2 || !reflect.DeepEqual(first, bus.Messages()[1]) {
		t.Fatal("request retry changed the reserved delivery")
	}
	ack, err := controllerevents.NewSecretInstalled(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Consume(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	settled, _ := requests.Get(t.Context(), request.ID)
	if settled.Request.Status != secrets.Fulfilled || settled.Installation.ID != ack.ID {
		t.Fatal("installation acknowledgement did not settle the request")
	}
	values.Set(nil)
	if err := consumer.Consume(t.Context(), ack); err != nil {
		t.Fatalf("exact historical acknowledgement failed after deletion: %v", err)
	}
	after, _ := requests.Get(t.Context(), request.ID)
	if !reflect.DeepEqual(settled, after) {
		t.Fatal("acknowledgement retry changed historical installation")
	}
	ack.ID = id.MessageID()
	if err := consumer.Consume(t.Context(), ack); !errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("conflicting acknowledgement accepted: %v", err)
	}
}

func TestSharedSecretReachesWaitingAndLaterAgentTypes(t *testing.T) {
	consumer, store, values, bus, requests := fixtures.SharedSecretConsumer(t)
	shared := values.Snapshot()
	values.Set(nil)
	for _, request := range requests[:2] {
		if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
			t.Fatal(err)
		}
	}
	if len(bus.Messages()) != 0 {
		t.Fatal("missing shared secret produced a delivery")
	}
	values.Set(shared)

	if err := consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	deliveries := bus.Messages()
	if len(deliveries) != 2 {
		t.Fatal("shared value did not reach both waiting agent types")
	}
	recipients := make(map[string]bool)
	for _, delivery := range deliveries {
		payload, err := agentevents.DecodeSecretDelivery(delivery)
		if err != nil || !reflect.DeepEqual(payload.Metadata, shared.Metadata) || !bytes.Equal(payload.Value, []byte("plaintext-secret")) {
			t.Fatalf("recipient did not receive the same shared version: %v", err)
		}
		recipients[delivery.AgentID] = true
	}
	if !recipients[requests[0].AgentID] || !recipients[requests[1].AgentID] {
		t.Fatal("shared deliveries lost their independent recipients")
	}
	ack, err := controllerevents.NewSecretInstalled(deliveries[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Consume(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	for _, delivery := range deliveries {
		record, err := store.Get(t.Context(), delivery.CorrelationID)
		if err != nil {
			t.Fatal(err)
		}
		want := secrets.Pending
		if delivery.ID == deliveries[0].ID {
			want = secrets.Fulfilled
		}
		if record.Request.Status != want {
			t.Fatal("one acknowledgement settled another recipient's request")
		}
	}

	if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, requests[2])); err != nil {
		t.Fatal(err)
	}

	deliveries = bus.Messages()
	if len(deliveries) != 3 || deliveries[2].AgentID != requests[2].AgentID {
		t.Fatal("later agent did not receive the stored shared value")
	}
	payload, err := agentevents.DecodeSecretDelivery(deliveries[2])
	if err != nil || !reflect.DeepEqual(payload.Metadata, shared.Metadata) || !bytes.Equal(payload.Value, []byte("plaintext-secret")) {
		t.Fatalf("later agent received a different shared version: %v", err)
	}
}

func TestSecretDeliveryRecoversUncertainReservationAndFailedPublication(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "failed publication"
		if uncertain {
			name = "uncertain reservation"
		}
		t.Run(name, func(t *testing.T) {
			consumer, requests, _, bus := fixtures.SecretConsumer(t)
			failure := errors.New("bus unavailable")
			if uncertain {
				requests.SetSaveError(secrets.ErrWriteUncertain)
				failure = secrets.ErrWriteUncertain
			} else {
				bus.FailPublish(failure)
			}
			request := fixtures.SecretSyncRequest()
			if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); !errors.Is(err, failure) {
				t.Fatalf("lost required failure: %v", err)
			}
			if len(bus.Messages()) != 0 {
				t.Fatal("failed save/publish reported a successful delivery")
			}
			reserved, _ := requests.Get(t.Context(), request.ID)
			if reserved.Delivery == nil {
				t.Fatal("no recoverable reserved snapshot")
			}
			requests.SetSaveError(nil)
			bus.FailPublish(nil)
			restarted := consumers.SecretEventConsumer{Bus: bus, Inventory: consumer.Inventory, Secrets: consumer.Secrets, Requests: requests, Decryptor: consumer.Decryptor}
			if err := restarted.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(bus.Messages()) != 1 || bus.Messages()[0].ID != reserved.Delivery.ID || !bus.Messages()[0].CreatedAt.Equal(reserved.Delivery.CreatedAt) {
				t.Fatal("recovery generated a replacement delivery")
			}
			payload, err := agentevents.DecodeSecretDelivery(bus.Messages()[0])
			if err != nil || !reflect.DeepEqual(payload.Metadata, reserved.Delivery.Secret.Metadata) || !bytes.Equal(payload.Value, []byte("plaintext-secret")) {
				t.Fatal("recovery changed the reserved metadata or value")
			}
		})
	}
}

func TestSecretRequestsRequireLiveOwnType(t *testing.T) {
	for _, name := range []string{"wrong type", "unknown recipient", "stale agent", "stopped agent", "future"} {
		t.Run(name, func(t *testing.T) {
			consumer, requests, _, bus := fixtures.SecretConsumer(t)
			request := fixtures.SecretSyncRequest()
			switch name {
			case "wrong type":
				request.AgentType = "other"
			case "unknown recipient":
				request.AgentID = "fixture-agent-ffffffff"
			case "stale agent":
				store := consumer.Inventory.(*fakes.Inventory)
				stale := store.Snapshot().Clone()
				stale.LeaseExpiresAt = time.Now().Add(-time.Second)
				store.Set(&stale)
			case "stopped agent":
				store := consumer.Inventory.(*fakes.Inventory)
				stopped := store.Snapshot().Clone()
				stopped.Status = agents.AgentStopped
				store.Set(&stopped)
			case "future":
				request.RequestedAt = time.Now().Add(time.Hour)
				request.ExpiresAt = request.RequestedAt.Add(time.Minute)
			}
			if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("invalid request accepted: %v", err)
			}
			pending, _ := requests.ListPending(t.Context())
			if len(pending) != 0 || len(bus.Messages()) != 0 {
				t.Fatal("invalid request reached persistence or publication")
			}
		})
	}
}

func TestSecretAcknowledgementMustMatchDeliveryAndReceiptDeadline(t *testing.T) {
	for _, name := range []string{"recipient", "delivery", "secret version", "future installation", "expired receipt"} {
		t.Run(name, func(t *testing.T) {
			consumer, requests, values, bus := fixtures.SecretConsumer(t)
			request := fixtures.SecretSyncRequest()
			var delivery messages.Message
			if name == "expired receipt" {
				now := time.Now().UTC()
				request.RequestedAt, request.ExpiresAt = now.Add(-2*time.Minute), now.Add(-time.Minute)
				secret := values.Snapshot().Clone()
				secret.Metadata.CreatedAt, secret.Metadata.UpdatedAt = now.Add(-3*time.Minute), now.Add(-3*time.Minute)
				secret = fixtures.EncryptedSecret(t, secret.Metadata, "plaintext-secret")
				values.Set(&secret)
				if err := requests.Create(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				reserved := secrets.Delivery{ID: id.MessageID(), RequestID: request.ID, AgentID: fixtures.AgentID,
					Secret: secret, CreatedAt: request.RequestedAt.Add(time.Second), ExpiresAt: request.ExpiresAt}
				if err := requests.SaveDelivery(t.Context(), reserved); err != nil {
					t.Fatal(err)
				}
				payload, _ := json.Marshal(agentevents.SecretDelivery{Metadata: secret.Metadata, Value: []byte("plaintext-secret")})
				delivery = messages.Message{ID: reserved.ID, Type: agentevents.SecretDelivered, AgentID: fixtures.AgentID,
					CorrelationID: request.ID, Payload: payload, CreatedAt: reserved.CreatedAt, ExpiresAt: reserved.ExpiresAt}
			} else {
				if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
					t.Fatal(err)
				}
				delivery = bus.Messages()[0]
			}
			secret, _ := agentevents.DecodeSecretDelivery(delivery)
			payload := controllerevents.SecretInstallation{RequestID: request.ID, SecretID: secret.Metadata.ID, SecretVersion: secret.Metadata.Version}
			ack, err := messages.NewMessage(controllerevents.SecretInstalled, fixtures.AgentID, payload)
			if err != nil {
				t.Fatal(err)
			}
			ack.CorrelationID = delivery.ID
			switch name {
			case "recipient":
				ack.AgentID = "fixture-agent-ffffffff"
			case "delivery":
				ack.CorrelationID = id.MessageID()
			case "secret version":
				payload.SecretVersion++
				ack.Payload, _ = json.Marshal(payload)
			case "future installation":
				ack.CreatedAt = time.Now().Add(time.Second)
			case "expired receipt":
				ack.CreatedAt = delivery.CreatedAt.Add(time.Second)
			}
			if err := consumer.Consume(t.Context(), ack); !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("invalid acknowledgement accepted: %v", err)
			}
			record, _ := requests.Get(t.Context(), request.ID)
			if record.Installation != nil || record.Request.Status != secrets.Pending {
				t.Fatal("invalid acknowledgement changed installation state")
			}
		})
	}
}

func TestSecretDeliveryRechecksRotationAfterReservation(t *testing.T) {
	consumer, requests, values, bus := fixtures.SecretConsumer(t)
	requests.AfterSave(func() {
		rotated := values.Snapshot().Clone()
		rotated.Metadata.Version++
		rotated.Metadata.UpdatedAt = time.Now().UTC()
		rotated = fixtures.EncryptedSecret(t, rotated.Metadata, "rotated-secret")
		values.Set(&rotated)
	})
	request := fixtures.SecretSyncRequest()
	if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
		t.Fatal(err)
	}
	record, _ := requests.Get(t.Context(), request.ID)
	if record.Request.Status != secrets.Cancelled || len(bus.Messages()) != 0 {
		t.Fatal("published a value superseded while reserving the delivery")
	}
}

func TestSecretDecryptionFailurePreservesReservationForRecovery(t *testing.T) {
	consumer, requests, values, bus := fixtures.SecretConsumer(t)
	cipher := consumer.Decryptor
	secret := values.Snapshot().Clone()
	failure := errors.New("private decryptor detail")
	decryptor := fakes.NewSecretDecryptor([]byte("plaintext-secret"), failure)
	consumer.Decryptor = decryptor
	request := fixtures.SecretSyncRequest()

	err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request))

	if err == nil || strings.Contains(err.Error(), failure.Error()) || errors.Is(err, failure) {
		t.Fatal("secret decryption failure was ignored or leaked private diagnostics")
	}
	if len(bus.Messages()) != 0 || !decryptor.Cleared() {
		t.Fatal("failed decryption published a delivery or retained plaintext")
	}
	reserved, err := requests.Get(t.Context(), request.ID)
	if err != nil || reserved.Delivery == nil || reserved.Request.Status != secrets.Pending ||
		!reflect.DeepEqual(reserved.Delivery.Secret, secret) || !reflect.DeepEqual(values.Snapshot(), &secret) {
		t.Fatal("decryption changed the reserved secret or its stored value")
	}
	restarted := consumers.SecretEventConsumer{
		Bus: bus, Inventory: consumer.Inventory, Secrets: consumer.Secrets, Requests: requests, Decryptor: cipher,
	}

	err = restarted.Reconcile(t.Context())

	if err != nil || len(bus.Messages()) != 1 {
		t.Fatal("corrected decryptor did not recover the pending delivery")
	}
	delivery := bus.Messages()[0]
	payload, err := agentevents.DecodeSecretDelivery(delivery)
	if err != nil || delivery.ID != reserved.Delivery.ID || !delivery.CreatedAt.Equal(reserved.Delivery.CreatedAt) ||
		!reflect.DeepEqual(payload.Metadata, reserved.Delivery.Secret.Metadata) || !bytes.Equal(payload.Value, []byte("plaintext-secret")) {
		t.Fatal("recovery changed the saved delivery metadata or value")
	}
}

func TestSecretDeliveryWithoutDecryptorWaitsThenFailsClosed(t *testing.T) {
	consumer, requests, values, bus := fixtures.SecretConsumer(t)
	cipher := consumer.Decryptor
	consumer.Decryptor = nil
	secret := values.Swap(nil)
	request := fixtures.SecretSyncRequest()
	if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
		t.Fatalf("missing value must remain pending without a decryptor: %v", err)
	}
	values.Set(secret)

	err := consumer.Reconcile(t.Context())

	if err == nil || len(bus.Messages()) != 0 {
		t.Fatal("missing decryptor published a secret")
	}
	reserved, err := requests.Get(t.Context(), request.ID)
	if err != nil || reserved.Delivery == nil || !reflect.DeepEqual(reserved.Delivery.Secret, *secret) {
		t.Fatal("missing decryptor lost the encrypted reservation")
	}
	consumer.Decryptor = cipher

	if err := consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	delivery := bus.Messages()[0]
	payload, err := agentevents.DecodeSecretDelivery(delivery)
	if err != nil || delivery.ID != reserved.Delivery.ID || !bytes.Equal(payload.Value, []byte("plaintext-secret")) {
		t.Fatal("restoring the decryptor did not recover the original reserved delivery")
	}
}
