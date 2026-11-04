package controller_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestTransientSecretDeliveryRetriesExactEvidenceAndSettlesOnlyItsReceipt(t *testing.T) {
	fixture := fixtures.TransientSecretConsumer(t)
	request := fixture.Request
	message := fixtures.SecretRequestMessage(t, request)
	if err := fixture.Consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Bus.Messages()) != 0 || fixture.Values.ReadCount() != 0 {
		t.Fatal("transient request resolved from the managed reusable value")
	}
	value := []byte("one-time-fixture-code")
	var heldPayload []byte
	fixture.Bus.BeforePublish(func(delivery messages.Message) {
		if heldPayload == nil {
			heldPayload = delivery.Payload
		}
	})
	publicationFailure := errors.New("transient delivery transport unavailable")
	fixture.Bus.FailPublish(publicationFailure)
	if err := fixture.Consumer.ProvideTransient(t.Context(), request.ID, value); !errors.Is(err, publicationFailure) {
		t.Fatalf("lost failed publication: %v", err)
	}
	fixture.Bus.FailPublish(nil)

	if err := fixture.Consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := fixture.Bus.Messages()[0]
	if err := fixture.Consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.ProvideTransient(t.Context(), request.ID, value); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.ProvideTransient(t.Context(), request.ID, []byte("different-code")); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("a retry replaced the retained code: %v", err)
	}

	for _, delivery := range fixture.Bus.Messages() {
		if !reflect.DeepEqual(first, delivery) {
			t.Fatal("transport retry changed transient delivery identity, bytes or lifetime")
		}
	}
	payload, err := agentevents.DecodeTransientSecretResponse(first)
	if err != nil || !bytes.Equal(payload.Value, value) {
		t.Fatalf("delivery did not carry its requested value: %v", err)
	}
	clear(payload.Value)
	ack, err := controllerevents.NewTransientSecretReceived(first)
	if err != nil {
		t.Fatal(err)
	}
	wrong := ack
	wrong.CorrelationID = id.New()
	if err := fixture.Consumer.Consume(t.Context(), wrong); !errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("mismatched receipt accepted: %v", err)
	}
	fixture.CompleteExecution(t)
	fixture.Bus.Reset()

	if err := fixture.Consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.ProvideTransient(t.Context(), request.ID, value); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("ended execution accepted another transient submission: %v", err)
	}
	if err := fixture.Consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}

	if len(heldPayload) == 0 || bytes.Count(heldPayload, []byte{0}) != len(heldPayload) {
		t.Fatal("completed execution retained its transient value")
	}
	if len(fixture.Bus.Messages()) != 0 {
		t.Fatal("completed execution republished a transient delivery")
	}
	pending, err := fixture.Requests.Get(t.Context(), request.ID)
	if err != nil || pending.Request.Status != secrets.Pending || pending.Request.Fulfillment != nil || pending.Delivery != nil {
		t.Fatalf("task completion discarded receipt evidence or invented receipt: %v", err)
	}
	if err := fixture.Consumer.Consume(t.Context(), wrong); !errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("mismatched late receipt accepted: %v", err)
	}

	if err := fixture.Consumer.Consume(t.Context(), ack); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.Consume(t.Context(), ack); err != nil {
		t.Fatalf("exact receipt replay failed: %v", err)
	}
	settled, err := fixture.Requests.Get(t.Context(), request.ID)
	if err != nil || settled.Request.Status != secrets.Fulfilled || settled.Request.Fulfillment.DeliveryID != first.ID ||
		settled.Delivery != nil || settled.Installation != nil || settled.Request.Fulfillment.SecretID != "" {
		t.Fatalf("receipt did not retain value-free delivery evidence: %v", err)
	}
	fixture.Bus.Reset()
	if err := fixture.Consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Bus.Messages()) != 1 {
		t.Fatal("completed request unexpectedly replayed a value")
	}
	terminal, err := agentevents.DecodeTransientSecretResponse(fixture.Bus.Messages()[0])
	if err != nil || terminal.Error != agentevents.TransientSecretInactive || terminal.Value != nil {
		t.Fatalf("completed request did not end the agent wait: %v", err)
	}
}

func TestTransientCancellationFencesLateRequestAndDelivery(t *testing.T) {
	fixture := fixtures.TransientSecretConsumer(t)
	request := fixture.Request
	cancel, err := controllerevents.NewSecretRequestCanceled(request)
	if err != nil {
		t.Fatal(err)
	}

	if err := fixture.Consumer.Consume(t.Context(), cancel); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Consumer.ProvideTransient(t.Context(), request.ID, []byte("late-code")); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("cancelled request accepted a value: %v", err)
	}

	record, err := fixture.Requests.Get(t.Context(), request.ID)
	if err != nil || record.Request.Status != secrets.Cancelled {
		t.Fatalf("late request reopened cancelled metadata: %v", err)
	}
	for _, response := range fixture.Bus.Messages() {
		payload, err := agentevents.DecodeTransientSecretResponse(response)
		if err != nil || payload.Value != nil || payload.Error != agentevents.TransientSecretCancelled {
			t.Fatalf("cancellation published a value or lost its status: %v", err)
		}
	}
}

func TestTransientReceiptUncertaintyClearsValueBeforeReconciliation(t *testing.T) {
	fixture := fixtures.TransientSecretConsumer(t)
	request := fixture.Request
	if err := fixture.Consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
		t.Fatal(err)
	}
	var heldPayload []byte
	fixture.Bus.BeforePublish(func(message messages.Message) { heldPayload = message.Payload })
	if err := fixture.Consumer.ProvideTransient(t.Context(), request.ID, []byte("uncertain-receipt-code")); err != nil {
		t.Fatal(err)
	}
	ack, err := controllerevents.NewTransientSecretReceived(fixture.Bus.Messages()[0])
	if err != nil {
		t.Fatal(err)
	}
	fixture.Requests.SetFulfillError(secrets.ErrWriteUncertain)

	if err := fixture.Consumer.Consume(t.Context(), ack); !errors.Is(err, secrets.ErrWriteUncertain) {
		t.Fatalf("lost uncertain receipt persistence: %v", err)
	}

	if len(heldPayload) == 0 || bytes.Count(heldPayload, []byte{0}) != len(heldPayload) {
		t.Fatal("receipt retained plaintext while its persistence was uncertain")
	}
	fixture.Requests.SetFulfillError(nil)
	fixture.Bus.Reset()
	if err := fixture.Consumer.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Bus.Messages()) != 0 {
		t.Fatal("receipt reconciliation resent the code")
	}
	if err := fixture.Consumer.Consume(t.Context(), ack); err != nil {
		t.Fatalf("confirmed historical receipt failed after reconciliation: %v", err)
	}
	saved, err := fixture.Requests.Get(t.Context(), request.ID)
	if err != nil || saved.Request.Status != secrets.Fulfilled || saved.Request.Fulfillment.DeliveryID != ack.CorrelationID || saved.Delivery != nil {
		t.Fatalf("receipt reconciliation lost value-free metadata: %v", err)
	}
}

func TestTransientRestartAndExpiryDiscardHeldValues(t *testing.T) {
	for _, reason := range []string{"restart", "expired", "execution finished then expired"} {
		t.Run(reason, func(t *testing.T) {
			fixture := fixtures.TransientSecretConsumer(t)
			request := fixture.Request
			if reason != "restart" {
				request.RequestedAt = time.Now().UTC().Add(-time.Minute)
				request.ExpiresAt = time.Now().UTC().Add(500 * time.Millisecond)
			}
			if err := fixture.Consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
				t.Fatal(err)
			}
			if err := fixture.Consumer.ProvideTransient(t.Context(), request.ID, []byte("discarded-code")); err != nil {
				t.Fatal(err)
			}
			var lateReceipt messages.Message
			if reason == "execution finished then expired" {
				var err error
				lateReceipt, err = controllerevents.NewTransientSecretReceived(fixture.Bus.Messages()[0])
				if err != nil {
					t.Fatal(err)
				}
				fixture.CompleteExecution(t)
				if err := fixture.Consumer.Reconcile(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			fixture.Bus.Reset()

			switch reason {
			case "restart":
				fixture.Consumer.ClearTransient()
				restarted := &consumers.SecretEventConsumer{Bus: fixture.Bus, Inventory: fixture.Consumer.Inventory,
					Secrets: fixture.Values, Requests: fixture.Requests, Executions: fixture.Executions, Operations: fixture.Operations}
				if err := restarted.InitializeTransient(t.Context()); err != nil {
					t.Fatal(err)
				}
				fixture.Consumer = restarted
			case "expired", "execution finished then expired":
				timer := time.NewTimer(time.Until(request.ExpiresAt))
				defer timer.Stop()
				select {
				case <-t.Context().Done():
					t.Fatal(t.Context().Err())
				case <-timer.C:
				}
			}
			if err := fixture.Consumer.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := fixture.Consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); err != nil {
				t.Fatal(err)
			}

			closed, err := fixture.Requests.Get(t.Context(), request.ID)
			if err != nil || closed.Request.Status == secrets.Pending {
				t.Fatalf("ended transient wait remained pending: %v", err)
			}
			if reason == "execution finished then expired" {
				if closed.Request.Status != secrets.RequestExpired || !closed.Request.ExpiresAt.Equal(request.ExpiresAt) {
					t.Fatal("receipt-only wait did not expire at its original deadline")
				}
				if err := fixture.Consumer.Consume(t.Context(), lateReceipt); !errors.Is(err, consumers.ErrInvalidMessage) {
					t.Fatalf("expired receipt-only wait accepted a late receipt: %v", err)
				}
			}
			for _, message := range fixture.Bus.Messages() {
				response, err := agentevents.DecodeTransientSecretResponse(message)
				if err != nil || response.Value != nil || response.Error == "" {
					t.Fatalf("ended wait replayed held value: %v", err)
				}
			}
			if fixture.Values.ReadCount() != 0 {
				t.Fatal("transient flow read managed reusable storage")
			}
		})
	}
}

func TestTransientRequestMustMatchAnActiveSavedExecution(t *testing.T) {
	fixture := fixtures.TransientSecretConsumer(t)
	request := fixture.Request
	request.TaskID = id.New()
	fixture.Executions.IgnoreLookup()

	if err := fixture.Consumer.Consume(t.Context(), fixtures.SecretRequestMessage(t, request)); !errors.Is(err, consumers.ErrInvalidMessage) {
		t.Fatalf("wrong execution work accepted: %v", err)
	}

	if _, err := fixture.Requests.Get(t.Context(), request.ID); !errors.Is(err, secrets.ErrUnknownRequest) {
		t.Fatal("invalid owner persisted transient metadata")
	}
	if len(fixture.Bus.Messages()) != 0 || fixture.Values.ReadCount() != 0 {
		t.Fatal("invalid execution accessed or delivered secret data")
	}
}
