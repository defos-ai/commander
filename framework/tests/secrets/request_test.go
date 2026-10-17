package secrets_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

func TestFulfillmentRecordsMatchingSecretAndDelivery(t *testing.T) {
	request := requestFixture()
	metadata := secretFixture().Metadata
	now := requestedAt.Add(time.Minute)
	fulfilled, err := request.Fulfill(metadata, deliveryID, now)
	if err != nil {
		t.Fatal(err)
	}
	want := secrets.Fulfillment{SecretID: metadata.ID, SecretVersion: metadata.Version, DeliveryID: deliveryID}
	if fulfilled.Status != secrets.Fulfilled || fulfilled.Fulfillment == nil || *fulfilled.Fulfillment != want || !fulfilled.ResolvedAt.Equal(now) {
		t.Fatalf("unexpected fulfillment: %+v", fulfilled)
	}
	if fulfilled.Key != request.Key || fulfilled.AgentID != request.AgentID {
		t.Fatal("fulfillment changed the request's scope or recipient")
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
		{"different agent type", func(s *secrets.Metadata) { s.Key.Scope.AgentType = "web" }},
		{"different name", func(s *secrets.Metadata) { s.Key.Name = "OTHER_TOKEN" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			mismatch := metadata
			test.change(&mismatch)
			if _, err := request.Fulfill(mismatch, deliveryID, now); err == nil {
				t.Fatal("secret for a different key fulfilled request")
			}
		})
	}
	if _, err := request.Fulfill(metadata, "", now); err == nil {
		t.Fatal("request fulfilled without a delivery identity")
	}
}

func TestFulfillmentRejectsUnavailableAndLateSecrets(t *testing.T) {
	request := requestFixture()
	metadata := secretFixture().Metadata
	for _, now := range []time.Time{request.ExpiresAt, request.ExpiresAt.Add(time.Nanosecond)} {
		if _, err := request.Fulfill(metadata, deliveryID, now); !errors.Is(err, secrets.ErrRequestExpired) {
			t.Fatalf("fulfillment at %s error = %v, want expired", now, err)
		}
	}
	now := requestedAt.Add(time.Minute)
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
			if _, err := request.Fulfill(test.metadata, deliveryID, now); err == nil {
				t.Fatal("unavailable secret fulfilled request")
			}
		})
	}
	if _, err := request.Fulfill(metadata, deliveryID, requestedAt.Add(-time.Nanosecond)); err == nil {
		t.Fatal("request fulfilled before it was made")
	}
}

func TestRequestLifecycleIsIndependentOfAvailability(t *testing.T) {
	request := requestFixture()
	metadata := secretFixture().Metadata
	if got := secrets.AvailabilityAt(&metadata, requestedAt); got != secrets.Available || request.Status != secrets.Pending {
		t.Fatal("available secret must coexist with a pending request")
	}
	expired, err := request.Expire(request.ExpiresAt)
	if err != nil || expired.Status != secrets.RequestExpired {
		t.Fatalf("expire request = %+v, %v", expired, err)
	}
	if got := secrets.AvailabilityAt(&metadata, request.ExpiresAt); got != secrets.Available {
		t.Fatal("request expiration changed secret availability")
	}
	metadata.ExpiresAt = requestedAt.Add(2 * time.Minute)
	fulfilled, err := request.Fulfill(metadata, deliveryID, requestedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := secrets.AvailabilityAt(&metadata, metadata.ExpiresAt); got != secrets.Expired {
		t.Fatalf("secret at expiration = %q", got)
	}
	if request.Status != secrets.Pending || fulfilled.Status != secrets.Fulfilled {
		t.Fatal("secret expiration changed a request lifecycle")
	}
	if _, err := fulfilled.Expire(request.ExpiresAt); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("expiring historical fulfillment error = %v, want closed", err)
	}
}

func TestFulfillmentReplayAndConflicts(t *testing.T) {
	metadata := secretFixture().Metadata
	fulfilled, err := requestFixture().Fulfill(metadata, deliveryID, requestedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fulfilled.Fulfill(metadata, deliveryID, metadata.ExpiresAt.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replayed, fulfilled) {
		t.Fatalf("historical acknowledgement replay = %+v, %v", replayed, err)
	}
	replayed.Fulfillment.SecretVersion++
	clone := fulfilled.Clone()
	clone.Fulfillment.DeliveryID = "00000000-0000-4000-8000-000000000004"
	if fulfilled.Fulfillment.SecretVersion != metadata.Version || fulfilled.Fulfillment.DeliveryID != deliveryID {
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
		{"rotated version", rotated, deliveryID},
		{"recreated identity", recreated, deliveryID},
		{"different delivery", metadata, "00000000-0000-4000-8000-000000000004"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := fulfilled.Fulfill(test.metadata, test.deliveryID, requestedAt.Add(2*time.Minute)); !errors.Is(err, secrets.ErrRequestClosed) {
				t.Fatalf("conflicting fulfillment error = %v, want closed", err)
			}
		})
	}
	if _, err := fulfilled.Cancel(requestedAt.Add(2 * time.Minute)); !errors.Is(err, secrets.ErrRequestClosed) {
		t.Fatalf("cancel fulfilled request error = %v, want closed", err)
	}
}

func TestCancellationAndExpirationSettleOnce(t *testing.T) {
	request := requestFixture()
	now := requestedAt.Add(time.Minute)
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
		if _, err := settled.Fulfill(secretFixture().Metadata, deliveryID, now); !errors.Is(err, secrets.ErrRequestClosed) {
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
