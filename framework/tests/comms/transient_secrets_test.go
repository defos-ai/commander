package comms_test

import (
	"bytes"
	"testing"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/id"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/comms"
)

func TestTransientSecretExchangeCannotChangeExecutionOrDelivery(t *testing.T) {
	request := fixtures.TransientSecretRequest()
	value := []byte("fixture-verification-code")
	defer clear(value)
	message, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := controllerevents.DecodeSecretSyncRequested(message)
	if err != nil || decoded.Request != request || message.CorrelationID != request.CommandID {
		t.Fatalf("transient request lost its original identity: %v", err)
	}
	message.Work.TaskID = id.New()
	if _, err := controllerevents.DecodeSecretSyncRequested(message); err == nil {
		t.Fatal("transient request accepted another execution's work")
	}
	delivery, err := agentevents.NewTransientSecretResponse(request, value, "")
	if err != nil {
		t.Fatal(err)
	}
	defer clear(delivery.Payload)
	payload, err := agentevents.DecodeTransientSecretResponse(delivery)
	if err != nil || !bytes.Equal(payload.Value, value) {
		t.Fatalf("transient response did not retain the supplied value: %v", err)
	}
	clear(payload.Value)
	other := request
	other.CommandID, other.ID = id.New(), id.New()
	if err := agentevents.ValidateTransientSecretResponseRequest(delivery, other); err == nil {
		t.Fatal("another request accepted the transient delivery")
	}

	receipt, err := controllerevents.NewTransientSecretReceived(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if err := controllerevents.ValidateTransientSecretReceivedDelivery(receipt, delivery); err != nil {
		t.Fatal(err)
	}
	receipt.CorrelationID = id.New()
	if err := controllerevents.ValidateTransientSecretReceivedDelivery(receipt, delivery); err == nil {
		t.Fatal("receipt acknowledged a different transient delivery")
	}
	cancellation, err := controllerevents.NewSecretRequestCanceled(request)
	if err != nil {
		t.Fatal(err)
	}
	cancellation.CreatedAt = request.ExpiresAt
	if _, err := controllerevents.DecodeSecretRequestCanceled(cancellation); err != nil {
		t.Fatalf("expiry prevented cancellation of the original wait: %v", err)
	}
	cancellation.Work.TaskID = id.New()
	if _, err := controllerevents.DecodeSecretRequestCanceled(cancellation); err == nil {
		t.Fatal("cancellation accepted another execution's work")
	}

	terminal, err := agentevents.NewTransientSecretResponse(request, nil, agentevents.TransientSecretExpired)
	if err != nil {
		t.Fatal(err)
	}
	terminal.CreatedAt = request.ExpiresAt
	if err := agentevents.ValidateTransientSecretResponseRequest(terminal, request); err != nil {
		t.Fatalf("agent could not receive expiry after the deadline: %v", err)
	}
	if _, err := agentevents.NewTransientSecretResponse(request, value, agentevents.TransientSecretExpired); err == nil {
		t.Fatal("terminal transient response retained a secret value")
	}
}
