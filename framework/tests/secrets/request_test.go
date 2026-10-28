package secrets_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/secrets"
)

func TestFulfillmentRecordsMatchingSecretAndDelivery(t *testing.T) {
	request := fixtures.Request()
	metadata := fixtures.Secret().Metadata
	now := fixtures.RequestedAt().Add(time.Minute)
	fulfilled, err := request.Fulfill(metadata, fixtures.DeliveryID, now)
	if err != nil {
		t.Fatal(err)
	}
	want := secrets.Fulfillment{SecretID: metadata.ID, SecretVersion: metadata.Version, DeliveryID: fixtures.DeliveryID}
	if fulfilled.Status != secrets.Fulfilled || fulfilled.Fulfillment == nil || *fulfilled.Fulfillment != want || !fulfilled.ResolvedAt.Equal(now) {
		t.Fatalf("unexpected fulfillment: %+v", fulfilled)
	}
	if fulfilled.Key != request.Key || fulfilled.AgentID != request.AgentID {
		t.Fatal("fulfillment changed the request's shared key or recipient")
	}
	if err := fulfilled.Validate(); err != nil {
		t.Fatal(err)
	}
	if request.Status != secrets.Pending || request.Fulfillment != nil || !request.ResolvedAt.IsZero() {
		t.Fatal("fulfillment changed the original pending request")
	}
	for _, test := range []struct {
		name   string
		change func(*secrets.Metadata)
	}{
		{"different name", func(s *secrets.Metadata) { s.Key.Name = "OTHER_TOKEN" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			mismatch := metadata
			test.change(&mismatch)
			if _, err := request.Fulfill(mismatch, fixtures.DeliveryID, now); err == nil {
				t.Fatal("secret for a different key fulfilled request")
			}
		})
	}
	if _, err := request.Fulfill(metadata, "", now); err == nil {
		t.Fatal("request fulfilled without a delivery identity")
	}
}

func TestFulfillmentRejectsUnavailableAndLateSecrets(t *testing.T) {
	request := fixtures.Request()
	metadata := fixtures.Secret().Metadata
	for _, now := range []time.Time{request.ExpiresAt, request.ExpiresAt.Add(time.Nanosecond)} {
		if _, err := request.Fulfill(metadata, fixtures.DeliveryID, now); !errors.Is(err, secrets.ErrRequestExpired) {
			t.Fatalf("fulfillment at %s error = %v, want expired", now, err)
		}
	}
	now := fixtures.RequestedAt().Add(time.Minute)
	expired := metadata
	expired.ExpiresAt = now
	future := metadata
	future.UpdatedAt = now.Add(time.Minute)
	for _, test := range []struct {
		name     string
		metadata secrets.Metadata
	}{
		{name: "missing"},
		{name: "expired", metadata: expired},
		{name: "future update", metadata: future},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := request.Fulfill(test.metadata, fixtures.DeliveryID, now); err == nil {
				t.Fatal("unavailable secret fulfilled request")
			}
		})
	}
	if _, err := request.Fulfill(metadata, fixtures.DeliveryID, fixtures.RequestedAt().Add(-time.Nanosecond)); err == nil {
		t.Fatal("request fulfilled before it was made")
	}
}

func TestFulfillmentReplayAndConflicts(t *testing.T) {
	metadata := fixtures.Secret().Metadata
	fulfilled, err := fixtures.Request().Fulfill(metadata, fixtures.DeliveryID, fixtures.RequestedAt().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fulfilled.Fulfill(metadata, fixtures.DeliveryID, metadata.ExpiresAt.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replayed, fulfilled) {
		t.Fatalf("historical acknowledgement replay = %+v, %v", replayed, err)
	}
	replayed.Fulfillment.SecretVersion++
	clone := fulfilled.Clone()
	clone.Fulfillment.DeliveryID = "00000000-0000-4000-8000-000000000004"
	if fulfilled.Fulfillment.SecretVersion != metadata.Version || fulfilled.Fulfillment.DeliveryID != fixtures.DeliveryID {
		t.Fatal("replayed or cloned fulfillment aliases historical evidence")
	}
	rotated := metadata
	rotated.Version++
	recreated := metadata
	recreated.ID = "00000000-0000-4000-8000-000000000005"
	for _, test := range []struct {
		name       string
		metadata   secrets.Metadata
		deliveryID string
	}{
		{"rotated version", rotated, fixtures.DeliveryID},
		{"recreated identity", recreated, fixtures.DeliveryID},
		{"different delivery", metadata, "00000000-0000-4000-8000-000000000004"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fulfilled.Fulfill(test.metadata, test.deliveryID, fixtures.RequestedAt().Add(2*time.Minute)); !errors.Is(err, secrets.ErrRequestClosed) {
				t.Fatalf("conflicting fulfillment error = %v, want closed", err)
			}
		})
	}
	if _, err := fulfilled.Cancel(fixtures.RequestedAt().Add(2 * time.Minute)); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("cancel fulfilled request error = %v, want closed", err)
	}
	if _, err := fulfilled.Expire(fixtures.Request().ExpiresAt); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("expire fulfilled request error = %v, want closed", err)
	}
}

func TestCancellationAndExpirationSettleOnce(t *testing.T) {
	request := fixtures.Request()
	now := fixtures.RequestedAt().Add(time.Minute)
	cancelled, err := request.Cancel(now)
	if err != nil || cancelled.Status != secrets.Cancelled || !cancelled.ResolvedAt.Equal(now) {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	replayed, err := cancelled.Cancel(request.ExpiresAt.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replayed, cancelled) {
		t.Fatalf("cancel replay = %+v, %v", replayed, err)
	}
	if _, err := request.Cancel(request.ExpiresAt); !errors.Is(err, secrets.ErrRequestExpired) {
		t.Fatalf("late cancellation error = %v, want expired", err)
	}
	if _, err := request.Expire(request.ExpiresAt.Add(-time.Nanosecond)); err == nil {
		t.Fatal("request expired before its deadline")
	}
	expired, err := request.Expire(request.ExpiresAt)
	if err != nil || expired.Status != secrets.RequestExpired || !expired.ResolvedAt.Equal(request.ExpiresAt) {
		t.Fatalf("expire = %+v, %v", expired, err)
	}
	replayed, err = expired.Expire(request.ExpiresAt.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replayed, expired) {
		t.Fatalf("expiry replay = %+v, %v", replayed, err)
	}
	for _, settled := range []secrets.Request{cancelled, expired} {
		if err := settled.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, err := settled.Fulfill(fixtures.Secret().Metadata, fixtures.DeliveryID, now); !errors.Is(err, secrets.ErrRequestClosed) {
			t.Fatalf("fulfill %s request error = %v, want closed", settled.Status, err)
		}
	}
	if _, err := cancelled.Expire(request.ExpiresAt); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("expire cancelled request error = %v, want closed", err)
	}
	if _, err := expired.Cancel(now); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("cancel expired request error = %v, want closed", err)
	}
	if request.Status != secrets.Pending || !request.ResolvedAt.IsZero() {
		t.Fatal("settlement mutated original request")
	}
}
