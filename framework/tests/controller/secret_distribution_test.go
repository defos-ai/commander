package controller_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller"
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
	if bytes.Contains(first.Payload, []byte("plaintext-secret")) {
		t.Fatal("delivery exposed plaintext")
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
		if err != nil || !reflect.DeepEqual(payload.Secret, *shared) {
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
	if err != nil || !reflect.DeepEqual(payload.Secret, *shared) {
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
			restarted := consumers.SecretEventConsumer{Bus: bus, Inventory: consumer.Inventory, Secrets: consumer.Secrets, Requests: requests}
			if err := restarted.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(bus.Messages()) != 1 || bus.Messages()[0].ID != reserved.Delivery.ID || !bus.Messages()[0].CreatedAt.Equal(reserved.Delivery.CreatedAt) {
				t.Fatal("recovery generated a replacement delivery")
			}
			payload, err := agentevents.DecodeSecretDelivery(bus.Messages()[0])
			if err != nil || !reflect.DeepEqual(payload.Secret, reserved.Delivery.Secret) {
				t.Fatal("recovery changed encrypted snapshot")
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

func TestSecretRequestsAllowOnDemandNamesWithinRegisteredType(t *testing.T) {
	consumer, requests, _, bus := fixtures.SecretConsumer(t)
	inventory := consumer.Inventory.(*fakes.Inventory)
	record := inventory.Snapshot().Clone()
	record.Actions[0].RequiredSecrets = nil
	record.Status = agents.AgentWorking
	inventory.Set(&record)
	request := fixtures.SecretSyncRequest()
	if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
		t.Fatal(err)
	}
	if len(bus.Messages()) != 1 || bus.Messages()[0].AgentID != request.AgentID {
		t.Fatal("on-demand request did not deliver to its registered recipient")
	}
	ack, err := controllerevents.NewSecretInstalled(bus.Messages()[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Consume(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	settled, err := requests.Get(t.Context(), request.ID)
	if err != nil || settled.Request.Status != secrets.Fulfilled {
		t.Fatalf("on-demand installation was not acknowledged: status=%s err=%v", settled.Request.Status, err)
	}
}

func TestSecretPendingRequestsAwaitAvailabilityAndRejectRotatedAcknowledgements(t *testing.T) {
	consumer, requests, values, bus := fixtures.SecretConsumer(t)
	initial := values.Swap(nil)
	request := fixtures.SecretSyncRequest()
	if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
		t.Fatal(err)
	}
	pending, _ := requests.Get(t.Context(), request.ID)
	if pending.Request.Status != secrets.Pending || pending.Delivery != nil || len(bus.Messages()) != 0 {
		t.Fatal("missing value did not remain pending")
	}
	values.Set(initial)
	if err := consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	ack, err := controllerevents.NewSecretInstalled(bus.Messages()[0])
	if err != nil {
		t.Fatal(err)
	}
	rotated := initial.Clone()
	rotated.Metadata.Version++
	rotated.Metadata.UpdatedAt = time.Now().UTC()
	rotated.Value.Ciphertext = []byte("new-ciphertext")
	values.Set(&rotated)
	if err := consumer.Consume(t.Context(), ack); !errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("superseded value was acknowledged: %v", err)
	}
	closed, _ := requests.Get(t.Context(), request.ID)
	if closed.Request.Status != secrets.Cancelled || closed.Installation != nil {
		t.Fatal("superseded pending delivery was not cancelled")
	}
	if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, fixtures.SecretSyncRequest())); err != nil {
		t.Fatal(err)
	}
	payload, err := agentevents.DecodeSecretDelivery(bus.Messages()[1])
	if err != nil || payload.Secret.Metadata.Version != 2 {
		t.Fatal("fresh request did not receive rotated value")
	}
}

func TestSecretReconcileCancelsUnavailableReservedValuesAndExpiresRequests(t *testing.T) {
	for _, name := range []string{"deleted", "expired secret", "expired request"} {
		t.Run(name, func(t *testing.T) {
			consumer, requests, values, bus := fixtures.SecretConsumer(t)
			request := fixtures.SecretSyncRequest()
			if name == "expired request" {
				request.RequestedAt = time.Now().Add(-2 * time.Minute)
				request.ExpiresAt = request.RequestedAt.Add(time.Minute)
				if err := requests.Create(t.Context(), request); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
					t.Fatal(err)
				}
				if name == "deleted" {
					values.Set(nil)
				} else {
					expired := values.Snapshot().Clone()
					expired.Metadata.ExpiresAt = time.Now().Add(-time.Second)
					values.Set(&expired)
				}
			}
			published := len(bus.Messages())
			if err := consumer.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			record, _ := requests.Get(t.Context(), request.ID)
			want := secrets.Cancelled
			if name == "expired request" {
				want = secrets.RequestExpired
			}
			if record.Request.Status != want || len(bus.Messages()) != published {
				t.Fatalf("invalid snapshot replayed: status=%s", record.Request.Status)
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
				values.Set(&secret)
				if err := requests.Create(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				reserved := secrets.Delivery{ID: id.MessageID(), RequestID: request.ID, AgentID: fixtures.AgentID,
					Secret: secret, CreatedAt: request.RequestedAt.Add(time.Second), ExpiresAt: request.ExpiresAt}
				if err := requests.SaveDelivery(t.Context(), reserved); err != nil {
					t.Fatal(err)
				}
				payload, _ := json.Marshal(agentevents.SecretDelivery{Secret: secret})
				delivery = messages.Message{ID: reserved.ID, Type: agentevents.SecretDelivered, AgentID: fixtures.AgentID,
					CorrelationID: request.ID, Payload: payload, CreatedAt: reserved.CreatedAt, ExpiresAt: reserved.ExpiresAt}
			} else {
				if err := consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
					t.Fatal(err)
				}
				delivery = bus.Messages()[0]
			}
			secret, _ := agentevents.DecodeSecretDelivery(delivery)
			payload := controllerevents.SecretInstallation{RequestID: request.ID, SecretID: secret.Secret.Metadata.ID, SecretVersion: secret.Secret.Metadata.Version}
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

func TestControllerRecoversSecretDeliveryAndConsumesInstallation(t *testing.T) {
	consumer, requests, _, _ := fixtures.SecretConsumer(t)
	request := fixtures.SecretSyncRequest()
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	bus := fakes.NewBus()
	c, err := controller.NewController(ctx, bus, controller.ControllerConfig{
		AgentStore: consumer.Inventory, ActionStore: fakes.UnusedActions{}, ExecutionStore: fakes.UnusedExecutions{}, ArtifactStore: fakes.UnusedArtifacts{},
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
	delivery := fixtures.Receive(t, ctx, bus.Commands())
	if delivery.Type != agentevents.SecretDelivered || delivery.CorrelationID != request.ID {
		t.Fatal("startup did not reconcile pending secret request")
	}
	ack, err := controllerevents.NewSecretInstalled(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if err := bus.Send(ctx, ack); err != nil {
		t.Fatal(err)
	}
	if fixtures.Receive(t, ctx, bus.Acknowledged()) != ack.ID {
		t.Fatal("unexpected bus acknowledgement")
	}
	record, _ := requests.Get(ctx, request.ID)
	if record.Request.Status != secrets.Fulfilled {
		t.Fatal("bus acknowledged installation before saving fulfillment")
	}
}

func TestControllerSecretRecoveryFailureStopsStartupAndClosesSubscription(t *testing.T) {
	consumer, requests, _, _ := fixtures.SecretConsumer(t)
	failure := errors.New("request storage unavailable")
	requests.SetListError(failure)
	bus := fakes.NewBus()
	c, err := controller.NewController(t.Context(), bus, controller.ControllerConfig{
		AgentStore: consumer.Inventory, ActionStore: fakes.UnusedActions{}, ExecutionStore: fakes.UnusedExecutions{}, ArtifactStore: fakes.UnusedArtifacts{},
		SecretStore: consumer.Secrets, SecretRequestStore: requests,
	})
	if c != nil || !errors.Is(err, failure) {
		t.Fatalf("startup ignored failed secret recovery: controller=%v err=%v", c != nil, err)
	}
	select {
	case <-bus.Closed():
	default:
		t.Fatal("failed startup leaked the event subscription")
	}
}
