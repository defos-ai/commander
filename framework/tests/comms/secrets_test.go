package comms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentevents "github.com/defos-ai/commander/framework/comms/clients/agent/events"
	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/comms/messages"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/comms"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/comms"
)

func TestSecretProtocolCorrelatesEncryptedDeliveryAndInstallation(t *testing.T) {
	request, secret := fixtures.SecretProtocol(t)
	sync, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	requested, err := controllerevents.DecodeSecretSyncRequested(sync)
	if err != nil || requested.Request.ID != request.ID || sync.ID != request.ID || sync.AgentID != request.AgentID {
		t.Fatal("synchronization did not preserve its requesting identity")
	}
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.CorrelationID != request.ID || delivery.AgentID != request.AgentID || !delivery.ExpiresAt.Equal(secret.Metadata.ExpiresAt) {
		t.Fatal("delivery did not match the recipient, request or earliest expiry")
	}
	installation, err := controllerevents.NewSecretInstalled(delivery)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := controllerevents.DecodeSecretInstalled(installation)
	if err != nil || installed.RequestID != request.ID || installed.SecretID != secret.Metadata.ID || installed.SecretVersion != secret.Metadata.Version || installation.CorrelationID != delivery.ID {
		t.Fatal("installation did not identify the exact request and encrypted delivery")
	}
	for _, message := range []messages.Message{sync, delivery, installation} {
		data, err := json.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(fixtures.SecretValue)) || bytes.Contains(data, []byte(fixtures.EncryptionKey)) {
			t.Fatal("secret protocol serialized plaintext or its encryption key")
		}
		if message.Work != (messages.WorkContext{}) {
			t.Fatal("secret protocol unexpectedly acquired operation work context")
		}
	}
	if request.Status != secretmodel.Pending || request.Fulfillment != nil {
		t.Fatal("preparing messages fulfilled a request before controller acceptance")
	}
	if err := controllerevents.ValidateSecretInstalledDelivery(installation, delivery); err != nil {
		t.Fatal(err)
	}
	fulfilled, err := request.Fulfill(secret.Metadata, installation.CorrelationID, installation.CreatedAt)
	if err != nil || fulfilled.Fulfillment == nil || fulfilled.Fulfillment.DeliveryID != delivery.ID {
		t.Fatal("validated acknowledgement could not be applied to its request")
	}
}

func TestSecretDeliverySnapshotsAreIndependent(t *testing.T) {
	request, secret := fixtures.SecretProtocol(t)
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(delivery.Payload)
	secret.Value.Ciphertext[0] ^= 0xff
	secret.Value.Nonce[0] ^= 0xff
	request.Key.Name = "DIFFERENT"
	decoded, err := agentevents.DecodeSecretDelivery(delivery)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Secret.Value.Ciphertext[0] ^= 0xff
	decoded.Secret.Value.Nonce[0] ^= 0xff
	if !bytes.Equal(delivery.Payload, before) {
		t.Fatal("changing inputs or decoded bytes mutated the prepared delivery")
	}
	again, err := agentevents.DecodeSecretDelivery(delivery)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewSecretCipher(fixtures.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(again.Secret)
	if err != nil || string(plaintext) != fixtures.SecretValue {
		t.Fatal("independent decoded delivery no longer decrypts to its original value")
	}
}

func TestSecretDeliveryRequiresMatchingPendingRequest(t *testing.T) {
	request, secret := fixtures.SecretProtocol(t)
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*secretmodel.Request)
	}{
		{"request identity", func(r *secretmodel.Request) { r.ID = id.New() }},
		{"recipient", func(r *secretmodel.Request) { r.AgentID = "builder-agent-87654321" }},
		{"key", func(r *secretmodel.Request) { r.Key.Name = "OTHER_TOKEN" }},
		{"deadline", func(r *secretmodel.Request) { r.ExpiresAt = delivery.ExpiresAt.Add(-time.Second) }},
		{"not yet requested", func(r *secretmodel.Request) { r.RequestedAt = delivery.CreatedAt.Add(time.Second) }},
		{"cancelled", func(r *secretmodel.Request) {
			r.Status, r.ResolvedAt = secretmodel.Cancelled, r.RequestedAt.Add(time.Second)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := request.Clone()
			test.change(&changed)
			if err := agentevents.ValidateSecretDeliveryRequest(delivery, changed); err == nil {
				t.Fatal("accepted delivery for a mismatched or closed request")
			}
		})
	}
	wrongKey := secret.Clone()
	wrongKey.Metadata.Key.Name = "OTHER_TOKEN"
	if _, err := agentevents.NewSecretDelivery(request, wrongKey); err == nil {
		t.Fatal("constructor delivered a different shared name")
	}
	expired := request.Clone()
	expired.ExpiresAt = expired.RequestedAt.Add(time.Second)
	if _, err := agentevents.NewSecretDelivery(expired, secret); err == nil {
		t.Fatal("constructor created a new delivery after the request expired")
	}
	noExpiry := secret.Clone()
	noExpiry.Metadata.ExpiresAt = time.Time{}
	unbounded, err := agentevents.NewSecretDelivery(request, noExpiry)
	if err != nil || !unbounded.ExpiresAt.Equal(request.ExpiresAt) {
		t.Fatal("secret without expiry must still use the request deadline")
	}
}

func TestSecretInstallationRejectsStaleOrMismatchedDelivery(t *testing.T) {
	request, secret := fixtures.SecretProtocol(t)
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := controllerevents.NewSecretInstalled(delivery)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*messages.Message, *controllerevents.SecretInstallation)
	}{
		{"recipient", func(m *messages.Message, _ *controllerevents.SecretInstallation) {
			m.AgentID = "builder-agent-87654321"
		}},
		{"delivery identity", func(m *messages.Message, _ *controllerevents.SecretInstallation) { m.CorrelationID = id.New() }},
		{"request identity", func(_ *messages.Message, p *controllerevents.SecretInstallation) { p.RequestID = id.New() }},
		{"secret identity", func(_ *messages.Message, p *controllerevents.SecretInstallation) { p.SecretID = id.New() }},
		{"secret version", func(_ *messages.Message, p *controllerevents.SecretInstallation) { p.SecretVersion++ }},
		{"before delivery", func(m *messages.Message, _ *controllerevents.SecretInstallation) {
			m.CreatedAt = delivery.CreatedAt.Add(-time.Second)
		}},
		{"after deadline", func(m *messages.Message, _ *controllerevents.SecretInstallation) { m.CreatedAt = delivery.ExpiresAt }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := installation
			payload, err := controllerevents.DecodeSecretInstalled(installation)
			if err != nil {
				t.Fatal(err)
			}
			test.change(&changed, &payload)
			changed.Payload = fixtures.SecretWirePayload(t, payload)
			if err := controllerevents.ValidateSecretInstalledDelivery(changed, delivery); err == nil {
				t.Fatal("accepted installation for a stale or mismatched delivery")
			}
		})
	}
	recreated := secret.Clone()
	recreated.Metadata.ID = id.New()
	newDelivery, err := agentevents.NewSecretDelivery(request, recreated)
	if err != nil {
		t.Fatal(err)
	}
	if err := controllerevents.ValidateSecretInstalledDelivery(installation, newDelivery); err == nil {
		t.Fatal("old installation accepted for a recreated secret identity")
	}
}

func TestSecretPublishersRejectInvalidWireDataAndContexts(t *testing.T) {
	request, secret := fixtures.SecretProtocol(t)
	sync, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := controllerevents.NewSecretInstalled(delivery)
	if err != nil {
		t.Fatal(err)
	}
	bus := &fakes.SecretBus{}
	for _, route := range []struct {
		name    string
		message messages.Message
		publish func(context.Context, messages.Message) error
	}{
		{"sync", sync, (controllerevents.Client{Bus: bus}).Publish},
		{"delivery", delivery, (agentevents.Client{Bus: bus}).Publish},
		{"installed", installed, (controllerevents.Client{Bus: bus}).Publish},
	} {
		t.Run(route.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := route.publish(nil, route.message); err == nil {
				t.Fatal("accepted nil context")
			}
			if err := route.publish(ctx, route.message); !errors.Is(err, context.Canceled) {
				t.Fatal("accepted canceled context")
			}
			for _, change := range []func(*messages.Message){
				func(m *messages.Message) { m.AgentID = "invalid" },
				func(m *messages.Message) { m.ID = "invalid" },
				func(m *messages.Message) { m.Work = messages.WorkContext{OperationID: id.New(), TaskID: id.New()} },
				func(m *messages.Message) { m.Payload = json.RawMessage(`{"secret":"` + fixtures.SecretValue + `"}`) },
				func(m *messages.Message) { m.Payload = json.RawMessage(`null`) },
				func(m *messages.Message) {
					m.Payload = append(bytes.Clone(m.Payload[:len(m.Payload)-1]), []byte(`,"unexpected":"`+fixtures.SecretValue+`"}`)...)
				},
			} {
				changed := route.message
				change(&changed)
				err := route.publish(t.Context(), changed)
				if err == nil {
					t.Fatal("invalid wire data was published")
				}
				if strings.Contains(err.Error(), fixtures.SecretValue) {
					t.Fatal("wire validation exposed payload data")
				}
			}
		})
	}
	if len(bus.Messages()) != 0 {
		t.Fatal("invalid messages reached the transport")
	}
}

func TestSecretEventRoutingPreservesReplayAndTransportErrors(t *testing.T) {
	request, secret := fixtures.SecretProtocol(t)
	sync, err := controllerevents.NewSecretSyncRequested(request)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := agentevents.NewSecretDelivery(request, secret)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := controllerevents.NewSecretInstalled(delivery)
	if err != nil {
		t.Fatal(err)
	}
	bus := &fakes.SecretBus{}
	for _, route := range []struct {
		message messages.Message
		topic   string
		publish func(context.Context, messages.Message) error
	}{
		{sync, messages.ControllerEvents(messages.DefaultControllerType), (controllerevents.Client{Bus: bus}).Publish},
		{delivery, messages.AgentEvents(request.AgentID), (agentevents.Client{Bus: bus}).Publish},
		{installed, messages.ControllerEvents(messages.DefaultControllerType), (controllerevents.Client{Bus: bus}).Publish},
	} {
		for range 2 {
			if err := route.publish(t.Context(), route.message); err != nil {
				t.Fatal(err)
			}
			last := bus.Messages()[len(bus.Messages())-1]
			if bus.Topics()[len(bus.Topics())-1] != route.topic || last.ID != route.message.ID || last.CorrelationID != route.message.CorrelationID || !bytes.Equal(last.Payload, route.message.Payload) {
				t.Fatal("publisher changed routing or prepared replay identity")
			}
		}
	}
	transportError := errors.New("transport unavailable")
	bus.Fail(transportError)
	before := len(bus.Messages())
	if err := (agentevents.Client{Bus: bus}).Publish(t.Context(), delivery); !errors.Is(err, transportError) || len(bus.Messages()) != before+1 {
		t.Fatal("publisher hid the transport failure or retried automatically")
	}
}
