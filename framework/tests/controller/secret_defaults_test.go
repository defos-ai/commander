package controller_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	controllerevents "github.com/defos-ai/commander/framework/comms/clients/controller/events"
	"github.com/defos-ai/commander/framework/controller/consumers"
	"github.com/defos-ai/commander/framework/domain/secrets"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/controller"
)

func TestReusableDefaultNeverReplacesExistingOrExpiredValue(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "expired"}[expired], func(t *testing.T) {
			consumer, _, values, bus := fixtures.SecretConsumer(t)
			original := values.Snapshot()
			if expired {
				original.Metadata.ExpiresAt = original.Metadata.UpdatedAt.Add(30 * time.Second)
				updated := fixtures.EncryptedSecret(t, original.Metadata, "plaintext-secret")
				values.Set(&updated)
				original = values.Snapshot()
			}
			message, err := controllerevents.NewSecretSyncRequested(fixtures.SecretSyncRequest(), []byte("ignored-default"))
			if err != nil {
				t.Fatal(err)
			}

			if err := consumer.Consume(t.Context(), message); err != nil {
				t.Fatal(err)
			}

			if values.WriteCount() != 0 || !reflect.DeepEqual(values.Snapshot(), original) {
				t.Fatal("default changed an existing shared value or its expiry")
			}
			if expired && len(bus.Messages()) != 0 {
				t.Fatal("expired shared value was renewed from a default")
			}
		})
	}
}

func TestReusableDefaultIsStoredOnceAndRequestReplayDoesNotRestoreDeletion(t *testing.T) {
	consumer, requests, values, bus := fixtures.SecretConsumer(t)
	values.Set(nil)
	request := fixtures.SecretSyncRequest()
	value := []byte("private-default-login")
	message, err := controllerevents.NewSecretSyncRequested(request, value)
	if err != nil {
		t.Fatal(err)
	}

	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}

	saved := values.Snapshot()
	if saved == nil || saved.Metadata.Version != 1 || values.WriteCount() != 1 || len(bus.Messages()) != 1 {
		t.Fatal("default was not saved and delivered through the shared secret exchange")
	}
	plaintext, err := consumer.Decryptor.Decrypt(*saved)
	defer clear(plaintext)
	if err != nil || !bytes.Equal(plaintext, value) {
		t.Fatal("saved default was not encrypted with the controller's configured cipher")
	}
	record, err := requests.Get(t.Context(), request.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(record)
	if err != nil || bytes.Contains(data, value) {
		t.Fatal("request persistence retained default plaintext")
	}
	values.Set(nil)
	bus.Reset()

	if err := consumer.Consume(t.Context(), message); err != nil {
		t.Fatal(err)
	}

	if values.Snapshot() != nil || values.WriteCount() != 1 || len(bus.Messages()) != 0 {
		t.Fatal("request replay restored a deleted default or republished its old value")
	}
}

func TestReusableDefaultUncertaintyIsNotReappliedByRequestRetry(t *testing.T) {
	for _, step := range []string{"request", "value"} {
		t.Run(step, func(t *testing.T) {
			consumer, requests, values, bus := fixtures.SecretConsumer(t)
			values.Set(nil)
			value := []byte("private-uncertain-default")
			writes := uint64(1)
			if step == "request" {
				requests.SetCreateError(secrets.ErrWriteUncertain)
				writes = 0
			} else {
				values.FailCreate(errors.Join(secrets.ErrWriteUncertain, errors.New(string(value))))
			}
			request := fixtures.SecretSyncRequest()
			message, err := controllerevents.NewSecretSyncRequested(request, value)
			if err != nil {
				t.Fatal(err)
			}

			err = consumer.Consume(t.Context(), message)

			if !errors.Is(err, secrets.ErrWriteUncertain) || strings.Contains(err.Error(), string(value)) || values.WriteCount() != writes || len(bus.Messages()) != 0 {
				t.Fatal("uncertain default storage was lost, exposed, retried or reported as delivery")
			}
			if _, err := requests.Get(t.Context(), request.ID); err != nil {
				t.Fatal("default mutation lacked its durable request identity")
			}
			values.FailCreate(nil)
			requests.SetCreateError(nil)

			if err := consumer.Consume(t.Context(), message); err != nil {
				t.Fatal(err)
			}

			if values.Snapshot() != nil || values.WriteCount() != writes || len(bus.Messages()) != 0 {
				t.Fatal("retry repeated an uncertain default mutation")
			}
		})
	}
}

func TestReusableDefaultRequiresLiveUnexpiredNewRequest(t *testing.T) {
	for _, scenario := range []string{"wrong type", "expired", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			consumer, requests, values, bus := fixtures.SecretConsumer(t)
			values.Set(nil)
			request := fixtures.SecretSyncRequest()
			switch scenario {
			case "wrong type":
				request.AgentType = "different"
			case "expired":
				request.RequestedAt = time.Now().UTC().Add(-2 * time.Minute)
				request.ExpiresAt = request.RequestedAt.Add(time.Minute)
			case "closed":
				if err := requests.Create(t.Context(), request); err != nil {
					t.Fatal(err)
				}
				if err := requests.Cancel(t.Context(), request.ID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			message, err := controllerevents.NewSecretSyncRequested(request, []byte("unaccepted-default"))
			if err != nil {
				t.Fatal(err)
			}

			err = consumer.Consume(t.Context(), message)

			if scenario != "closed" && !errors.Is(err, consumers.ErrInvalidMessage) {
				t.Fatal("invalid default request was accepted")
			}
			if scenario == "closed" && err != nil {
				t.Fatal(err)
			}
			if values.WriteCount() != 0 || values.Snapshot() != nil || len(bus.Messages()) != 0 {
				t.Fatal("unaccepted default request changed shared secret state")
			}
		})
	}
}
