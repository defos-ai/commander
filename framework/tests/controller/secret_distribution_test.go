package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/comms"
	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/agents"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
)

func TestSecretDeliveryPersistsBeforePublishingAndReplaysExactEvidence(t *testing.T) {
	consumer, requests, values, bus := secretConsumerFixture(t)
	request := secretSyncRequest()
	bus.before = func(message messages.Message) {
		record, err := requests.Get(t.Context(), request.ID)
		if err != nil || record.Delivery == nil || record.Delivery.ID != message.ID {
			t.Fatalf("publication preceded durable reservation: %v", err)
		}
	}
	requestMessage := secretRequestMessage(t, request)
	if err := consumer.Consume(t.Context(), requestMessage); err != nil {
		t.Fatal(err)
	}
	first := bus.messages[0]
	if bus.topics[0] != messages.AgentEvents(request.AgentID) {
		t.Fatal("secret delivery used the action command channel")
	}
	if bytes.Contains(first.Payload, []byte("plaintext-secret")) {
		t.Fatal("delivery exposed plaintext")
	}
	if err := consumer.Consume(t.Context(), requestMessage); err != nil {
		t.Fatal(err)
	}
	if len(bus.messages) != 2 || !reflect.DeepEqual(first, bus.messages[1]) {
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
	values.record.Store(nil)
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

func TestSecretDeliveryRecoversUncertainReservationAndFailedPublication(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		name := "failed publication"
		if uncertain {
			name = "uncertain reservation"
		}
		t.Run(name, func(t *testing.T) {
			consumer, requests, _, bus := secretConsumerFixture(t)
			failure := errors.New("bus unavailable")
			if uncertain {
				requests.saveError = secrets.ErrWriteUncertain
				failure = secrets.ErrWriteUncertain
			} else {
				bus.failure = failure
			}
			request := secretSyncRequest()
			if err := consumer.Consume(t.Context(), secretRequestMessage(t, request)); !errors.Is(err, failure) {
				t.Fatalf("lost required failure: %v", err)
			}
			if len(bus.messages) != 0 {
				t.Fatal("failed save/publish reported a successful delivery")
			}
			reserved, _ := requests.Get(t.Context(), request.ID)
			if reserved.Delivery == nil {
				t.Fatal("no recoverable reserved snapshot")
			}
			requests.saveError, bus.failure = nil, nil
			restarted := consumers.SecretEventConsumer{Bus: bus, Inventory: consumer.Inventory, Secrets: consumer.Secrets, Requests: requests}
			if err := restarted.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(bus.messages) != 1 || bus.messages[0].ID != reserved.Delivery.ID || !bus.messages[0].CreatedAt.Equal(reserved.Delivery.CreatedAt) {
				t.Fatal("recovery generated a replacement delivery")
			}
			payload, err := agentevents.DecodeSecretDelivery(bus.messages[0])
			if err != nil || !reflect.DeepEqual(payload.Secret, reserved.Delivery.Secret) {
				t.Fatal("recovery changed encrypted snapshot")
			}
		})
	}
}

func TestSecretRequestsRequireLiveDeclaredOwnType(t *testing.T) {
	for _, name := range []string{"wrong type", "undeclared", "unknown recipient", "stale agent", "operation", "future"} {
		t.Run(name, func(t *testing.T) {
			consumer, requests, _, bus := secretConsumerFixture(t)
			request := secretSyncRequest()
			switch name {
			case "wrong type":
				request.Key.Scope.AgentType = "other"
			case "undeclared":
				request.Key.Name = "OTHER_TOKEN"
			case "unknown recipient":
				request.AgentID = "fixture-agent-ffffffff"
			case "stale agent":
				store := consumer.Inventory.(*inventory)
				stale := store.record.Load().Clone()
				stale.LeaseExpiresAt = time.Now().Add(-time.Second)
				store.record.Store(&stale)
			case "operation":
				request.OperationID = id.OperationID()
			case "future":
				request.RequestedAt = time.Now().Add(time.Hour)
				request.ExpiresAt = request.RequestedAt.Add(time.Minute)
			}
			if err := consumer.Consume(t.Context(), secretRequestMessage(t, request)); !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatalf("invalid request accepted: %v", err)
			}
			pending, _ := requests.ListPending(t.Context())
			if len(pending) != 0 || len(bus.messages) != 0 {
				t.Fatal("invalid request reached persistence or publication")
			}
		})
	}
}

func TestSecretPendingRequestsAwaitAvailabilityAndRejectRotatedAcknowledgements(t *testing.T) {
	consumer, requests, values, bus := secretConsumerFixture(t)
	initial := values.record.Swap(nil)
	request := secretSyncRequest()
	if err := consumer.Consume(t.Context(), secretRequestMessage(t, request)); err != nil {
		t.Fatal(err)
	}
	pending, _ := requests.Get(t.Context(), request.ID)
	if pending.Request.Status != secrets.Pending || pending.Delivery != nil || len(bus.messages) != 0 {
		t.Fatal("missing value did not remain pending")
	}
	values.record.Store(initial)
	if err := consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	ack, err := controllerevents.NewSecretInstalled(bus.messages[0])
	if err != nil {
		t.Fatal(err)
	}
	rotated := initial.Clone()
	rotated.Metadata.Version++
	rotated.Metadata.UpdatedAt = time.Now().UTC()
	rotated.Value.Ciphertext = []byte("new-ciphertext")
	values.record.Store(&rotated)
	if err := consumer.Consume(t.Context(), ack); !errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("superseded value was acknowledged: %v", err)
	}
	closed, _ := requests.Get(t.Context(), request.ID)
	if closed.Request.Status != secrets.Cancelled || closed.Installation != nil {
		t.Fatal("superseded pending delivery was not cancelled")
	}
	if err := consumer.Consume(t.Context(), secretRequestMessage(t, secretSyncRequest())); err != nil {
		t.Fatal(err)
	}
	payload, err := agentevents.DecodeSecretDelivery(bus.messages[1])
	if err != nil || payload.Secret.Metadata.Version != 2 {
		t.Fatal("fresh request did not receive rotated value")
	}
}

func TestSecretReconcileCancelsUnavailableReservedValuesAndExpiresRequests(t *testing.T) {
	for _, name := range []string{"deleted", "expired secret", "expired request"} {
		t.Run(name, func(t *testing.T) {
			consumer, requests, values, bus := secretConsumerFixture(t)
			request := secretSyncRequest()
			if name == "expired request" {
				request.RequestedAt = time.Now().Add(-2 * time.Minute)
				request.ExpiresAt = request.RequestedAt.Add(time.Minute)
				if err := requests.Create(t.Context(), request); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := consumer.Consume(t.Context(), secretRequestMessage(t, request)); err != nil {
					t.Fatal(err)
				}
				if name == "deleted" {
					values.record.Store(nil)
				} else {
					expired := values.record.Load().Clone()
					expired.Metadata.ExpiresAt = time.Now().Add(-time.Second)
					values.record.Store(&expired)
				}
			}
			published := len(bus.messages)
			if err := consumer.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			record, _ := requests.Get(t.Context(), request.ID)
			want := secrets.Cancelled
			if name == "expired request" {
				want = secrets.RequestExpired
			}
			if record.Request.Status != want || len(bus.messages) != published {
				t.Fatalf("invalid snapshot replayed: status=%s", record.Request.Status)
			}
		})
	}
}

func TestSecretAcknowledgementMustMatchDeliveryAndReceiptDeadline(t *testing.T) {
	for _, name := range []string{"recipient", "delivery", "secret version", "future installation", "expired receipt"} {
		t.Run(name, func(t *testing.T) {
			consumer, requests, values, bus := secretConsumerFixture(t)
			request := secretSyncRequest()
			var delivery messages.Message
			if name == "expired receipt" {
				now := time.Now().UTC()
				request.RequestedAt, request.ExpiresAt = now.Add(-2*time.Minute), now.Add(-time.Minute)
				secret := values.record.Load().Clone()
				secret.Metadata.CreatedAt, secret.Metadata.UpdatedAt = now.Add(-3*time.Minute), now.Add(-3*time.Minute)
				values.record.Store(&secret)
				if err := requests.Create(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				reserved := secrets.Delivery{ID: id.MessageID(), RequestID: request.ID, AgentID: agentID,
					Secret: secret, CreatedAt: request.RequestedAt.Add(time.Second), ExpiresAt: request.ExpiresAt}
				if err := requests.SaveDelivery(t.Context(), reserved); err != nil {
					t.Fatal(err)
				}
				payload, _ := json.Marshal(agentevents.SecretDelivery{Secret: secret})
				delivery = messages.Message{ID: reserved.ID, Type: agentevents.SecretDelivered, AgentID: agentID,
					CorrelationID: request.ID, Payload: payload, CreatedAt: reserved.CreatedAt, ExpiresAt: reserved.ExpiresAt}
			} else {
				if err := consumer.Consume(t.Context(), secretRequestMessage(t, request)); err != nil {
					t.Fatal(err)
				}
				delivery = bus.messages[0]
			}
			secret, _ := agentevents.DecodeSecretDelivery(delivery)
			payload := controllerevents.SecretInstallation{RequestID: request.ID, SecretID: secret.Secret.Metadata.ID, SecretVersion: secret.Secret.Metadata.Version}
			ack, err := messages.NewMessage(controllerevents.SecretInstalled, agentID, payload)
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
	consumer, requests, values, bus := secretConsumerFixture(t)
	requests.afterSave = func() {
		rotated := values.record.Load().Clone()
		rotated.Metadata.Version++
		rotated.Metadata.UpdatedAt = time.Now().UTC()
		values.record.Store(&rotated)
	}
	request := secretSyncRequest()
	if err := consumer.Consume(t.Context(), secretRequestMessage(t, request)); err != nil {
		t.Fatal(err)
	}
	record, _ := requests.Get(t.Context(), request.ID)
	if record.Request.Status != secrets.Cancelled || len(bus.messages) != 0 {
		t.Fatal("published a value superseded while reserving the delivery")
	}
}

func TestControllerRecoversSecretDeliveryAndConsumesInstallation(t *testing.T) {
	consumer, requests, _, _ := secretConsumerFixture(t)
	request := secretSyncRequest()
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	bus := &testBus{events: make(chan messages.Message, 16), commands: make(chan messages.Message, 16), acked: make(chan string, 16), closed: make(chan struct{})}
	c, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: consumer.Inventory, ActionStore: unusedActions{}, ExecutionStore: unusedExecutions{}, ArtifactStore: unusedArtifacts{},
		SecretStore: consumer.Secrets, SecretRequestStore: requests, LeaseSweepInterval: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		if err := c.Close(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	delivery := receive(t, ctx, bus.commands)
	if delivery.Type != agentevents.SecretDelivered || delivery.CorrelationID != request.ID {
		t.Fatal("startup did not reconcile pending secret request")
	}
	ack, err := controllerevents.NewSecretInstalled(delivery)
	if err != nil {
		t.Fatal(err)
	}
	bus.events <- ack
	if receive(t, ctx, bus.acked) != ack.ID {
		t.Fatal("unexpected bus acknowledgement")
	}
	record, _ := requests.Get(ctx, request.ID)
	if record.Request.Status != secrets.Fulfilled {
		t.Fatal("bus acknowledged installation before saving fulfillment")
	}
}

func TestControllerSecretRecoveryFailureStopsStartupAndClosesSubscription(t *testing.T) {
	consumer, requests, _, _ := secretConsumerFixture(t)
	failure := errors.New("request storage unavailable")
	requests.listError = failure
	bus := &testBus{closed: make(chan struct{})}
	c, err := controller.NewController(t.Context(), bus, controller.ControllerConfig{
		AgentStore: consumer.Inventory, ActionStore: unusedActions{}, ExecutionStore: unusedExecutions{}, ArtifactStore: unusedArtifacts{},
		SecretStore: consumer.Secrets, SecretRequestStore: requests,
	})
	if c != nil || !errors.Is(err, failure) {
		t.Fatalf("startup ignored failed secret recovery: controller=%v err=%v", c != nil, err)
	}
	select {
	case <-bus.closed:
	default:
		t.Fatal("failed startup leaked the event subscription")
	}
}

func secretConsumerFixture(t *testing.T) (consumers.SecretEventConsumer, *secretRequestMemory, *secretValueMemory, *secretPublishBus) {
	t.Helper()
	now := time.Now().UTC()
	snapshot := observationSnapshot()
	snapshot.Actions[0].RequiredSecrets = []string{"API_TOKEN"}
	inventory := &inventory{}
	inventory.record.Store(&agents.Agent{AgentSnapshot: snapshot, LastHeartbeat: now, LeaseExpiresAt: now.Add(time.Minute)})
	requests := &secretRequestMemory{records: make(map[string]secrets.RequestRecord)}
	values := &secretValueMemory{}
	values.record.Store(&secrets.Secret{
		Metadata: secrets.Metadata{ID: id.MessageID(), Key: secretSyncRequest().Key, Version: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
		Value:    secrets.EncryptedValue{Format: "test-envelope", KeyID: "test-key", Nonce: []byte("nonce"), Ciphertext: []byte("ciphertext")},
	})
	bus := &secretPublishBus{}
	return consumers.SecretEventConsumer{Bus: bus, Inventory: inventory, Secrets: values, Requests: requests}, requests, values, bus
}

func secretSyncRequest() secrets.Request {
	now := time.Now().UTC()
	return secrets.Request{
		ID: id.MessageID(), AgentID: agentID, Key: secrets.Key{Scope: secrets.Scope{AgentType: "fixture"}, Name: "API_TOKEN"},
		Status: secrets.Pending, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}

func secretRequestMessage(t *testing.T, request secrets.Request) messages.Message {
	t.Helper()
	message, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

type secretPublishBus struct {
	comms.Bus
	messages []messages.Message
	topics   []string
	before   func(messages.Message)
	failure  error
}

func (bus *secretPublishBus) Publish(_ context.Context, topic string, message messages.Message) error {
	if bus.before != nil {
		bus.before(message)
	}
	if bus.failure != nil {
		return bus.failure
	}
	bus.messages, bus.topics = append(bus.messages, message), append(bus.topics, topic)
	return nil
}

type secretValueMemory struct {
	secrets.SecretStore
	record atomic.Pointer[secrets.Secret]
}

func (store *secretValueMemory) Get(ctx context.Context, key secrets.Key) (secrets.Secret, error) {
	if err := ctx.Err(); err != nil {
		return secrets.Secret{}, err
	}
	if record := store.record.Load(); record != nil && record.Metadata.Key == key {
		return record.Clone(), nil
	}
	return secrets.Secret{}, secrets.ErrUnknownSecret
}

type secretRequestMemory struct {
	mu        sync.Mutex
	records   map[string]secrets.RequestRecord
	saveError error
	listError error
	afterSave func()
}

func (store *secretRequestMemory) Create(ctx context.Context, request secrets.Request) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if saved, ok := store.records[request.ID]; ok {
		if !saved.MatchesRequest(request) {
			return secrets.ErrRequestConflict
		}
		return nil
	}
	record, err := secrets.NewRequestRecord(request)
	if err == nil {
		store.records[request.ID] = record
	}
	return err
}

func (store *secretRequestMemory) Get(ctx context.Context, requestID string) (secrets.RequestRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return secrets.RequestRecord{}, err
	}
	record, ok := store.records[requestID]
	if !ok {
		return secrets.RequestRecord{}, secrets.ErrUnknownRequest
	}
	return record.Clone(), nil
}

func (store *secretRequestMemory) ListPending(ctx context.Context) ([]secrets.RequestRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if store.listError != nil {
		return nil, store.listError
	}
	var records []secrets.RequestRecord
	for _, record := range store.records {
		if record.Request.Status == secrets.Pending {
			records = append(records, record.Clone())
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Request.ID < records[j].Request.ID })
	return records, nil
}

func (store *secretRequestMemory) SaveDelivery(ctx context.Context, delivery secrets.Delivery) error {
	err := store.mutate(ctx, delivery.RequestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		for requestID, saved := range store.records {
			if requestID != delivery.RequestID && saved.Delivery != nil && saved.Delivery.ID == delivery.ID {
				return secrets.RequestRecord{}, secrets.ErrRequestConflict
			}
		}
		return record.WithDelivery(delivery)
	})
	if err != nil {
		return err
	}
	if store.afterSave != nil {
		store.afterSave()
	}
	return store.saveError
}

func (store *secretRequestMemory) Acknowledge(ctx context.Context, installation secrets.Installation, now time.Time) error {
	return store.mutate(ctx, installation.RequestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		return record.Acknowledge(installation, now)
	})
}

func (store *secretRequestMemory) Cancel(ctx context.Context, requestID string, now time.Time) error {
	return store.mutate(ctx, requestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		return record.Cancel(now)
	})
}

func (store *secretRequestMemory) Expire(ctx context.Context, requestID string, now time.Time) error {
	return store.mutate(ctx, requestID, func(record secrets.RequestRecord) (secrets.RequestRecord, error) {
		return record.Expire(now)
	})
}

func (store *secretRequestMemory) mutate(ctx context.Context, requestID string, apply func(secrets.RequestRecord) (secrets.RequestRecord, error)) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	record, ok := store.records[requestID]
	if !ok {
		return secrets.ErrUnknownRequest
	}
	next, err := apply(record.Clone())
	if err == nil {
		store.records[requestID] = next.Clone()
	}
	return err
}
