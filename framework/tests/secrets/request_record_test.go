package secrets_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/secrets"
)

func TestRequestRecordRequiresPendingCreationAndPreservesOriginalIdentity(t *testing.T) {
	request := fixtures.Request()
	record := fixtures.PendingRecord(t)
	if !record.MatchesRequest(request) || record.Delivery != nil || record.Installation != nil {
		t.Fatal("new request record did not retain the pending identity")
	}
	cancelled, err := request.Cancel(fixtures.RequestedAt().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.NewRequestRecord(cancelled); err == nil {
		t.Fatal("terminal request accepted as new")
	}
	if record.MatchesRequest(cancelled) {
		t.Fatal("settled request accepted as original pending input")
	}
	for _, mutate := range []func(*secrets.Request){
		func(r *secrets.Request) { r.ID = fixtures.InstallationID },
		func(r *secrets.Request) { r.AgentID = "builder-agent-87654321" },
		func(r *secrets.Request) { r.Key.Scope.AgentType = "another-agent" },
		func(r *secrets.Request) { r.Key.Name = "OTHER_TOKEN" },
		func(r *secrets.Request) { r.RequestedAt = r.RequestedAt.Add(-time.Second) },
		func(r *secrets.Request) { r.ExpiresAt = r.ExpiresAt.Add(time.Second) },
	} {
		changed := request.Clone()
		mutate(&changed)
		if record.MatchesRequest(changed) {
			t.Fatal("changed request matched the original identity")
		}
	}
	withDelivery := fixtures.DeliveryRecord(t)
	ack := fixtures.Installation(*withDelivery.Delivery)
	fulfilled, err := withDelivery.Acknowledge(ack, ack.InstalledAt.Add(time.Second))
	if err != nil || !fulfilled.MatchesRequest(request) {
		t.Fatalf("progress changed original request matching: %v", err)
	}
}

func TestRequestRecordDeliveryIsImmutableAndBoundToRequest(t *testing.T) {
	pending := fixtures.PendingRecord(t)
	delivery := fixtures.Delivery(t)
	for _, test := range []struct {
		name   string
		change func(*secrets.Delivery)
	}{
		{"request", func(d *secrets.Delivery) { d.RequestID = fixtures.InstallationID }},
		{"recipient", func(d *secrets.Delivery) { d.AgentID = "builder-agent-87654321" }},
		{"agent type", func(d *secrets.Delivery) { d.Secret.Metadata.Key.Scope.AgentType = "other-agent" }},
		{"name", func(d *secrets.Delivery) { d.Secret.Metadata.Key.Name = "OTHER_TOKEN" }},
		{"before request", func(d *secrets.Delivery) { d.CreatedAt = fixtures.RequestedAt().Add(-time.Second) }},
		{"request deadline", func(d *secrets.Delivery) { d.ExpiresAt = d.ExpiresAt.Add(time.Second) }},
		{"shortened deadline", func(d *secrets.Delivery) { d.ExpiresAt = d.ExpiresAt.Add(-time.Second) }},
		{"future value", func(d *secrets.Delivery) { d.Secret.Metadata.UpdatedAt = d.CreatedAt.Add(time.Second) }},
		{"expired value", func(d *secrets.Delivery) { d.Secret.Metadata.ExpiresAt = d.CreatedAt }},
		{"value deadline", func(d *secrets.Delivery) { d.Secret.Metadata.ExpiresAt = d.ExpiresAt.Add(-time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := delivery.Clone()
			test.change(&changed)
			if _, err := pending.WithDelivery(changed); err == nil {
				t.Fatal("mismatched or invalid delivery accepted")
			}
		})
	}
	record, err := pending.WithDelivery(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Delivery != nil {
		t.Fatal("saving delivery mutated the original record")
	}
	delivery.Secret.Value.Ciphertext[0] ^= 1
	if bytes.Equal(record.Delivery.Secret.Value.Ciphertext, delivery.Secret.Value.Ciphertext) {
		t.Fatal("saving delivery retained caller-owned ciphertext")
	}
	replayed, err := record.WithDelivery(*record.Delivery)
	if err != nil || !reflect.DeepEqual(replayed, record) {
		t.Fatalf("identical delivery replay changed saved state: %v", err)
	}
	for _, change := range []func(*secrets.Delivery){
		func(d *secrets.Delivery) { d.ID = fixtures.InstallationID },
		func(d *secrets.Delivery) { d.Secret.Metadata.Version++ },
		func(d *secrets.Delivery) { d.Secret.Metadata.ID = fixtures.InstallationID },
		func(d *secrets.Delivery) { d.Secret.Value.Ciphertext[0] ^= 1 },
		func(d *secrets.Delivery) { d.Secret.Value.Nonce[0] ^= 1 },
		func(d *secrets.Delivery) { d.CreatedAt = d.CreatedAt.Add(time.Second) },
	} {
		changed := record.Delivery.Clone()
		change(&changed)
		if _, err := record.WithDelivery(changed); !errors.Is(err, secrets.ErrRequestConflict) {
			t.Fatalf("changed delivery replaced reserved snapshot: %v", err)
		}
	}

	short := fixtures.Delivery(t)
	short.Secret.Metadata.ExpiresAt = fixtures.RequestedAt().Add(5 * time.Minute)
	short.ExpiresAt = short.Secret.Metadata.ExpiresAt
	if _, err := pending.WithDelivery(short); err != nil {
		t.Fatalf("secret expiration could not bound delivery deadline: %v", err)
	}
}

func TestRequestRecordAcknowledgementRequiresExactEvidenceAndReceiptTime(t *testing.T) {
	record := fixtures.DeliveryRecord(t)
	installation := fixtures.Installation(*record.Delivery)
	received := installation.InstalledAt.Add(time.Second)
	if _, err := fixtures.PendingRecord(t).Acknowledge(installation, received); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("acknowledgement without saved delivery accepted: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*secrets.Installation)
	}{
		{"request", func(i *secrets.Installation) { i.RequestID = fixtures.DeliveryID }},
		{"delivery", func(i *secrets.Installation) { i.DeliveryID = fixtures.InstallationID }},
		{"recipient", func(i *secrets.Installation) { i.AgentID = "builder-agent-87654321" }},
		{"secret identity", func(i *secrets.Installation) { i.SecretID = fixtures.InstallationID }},
		{"version", func(i *secrets.Installation) { i.SecretVersion++ }},
		{"before delivery", func(i *secrets.Installation) { i.InstalledAt = record.Delivery.CreatedAt.Add(-time.Nanosecond) }},
		{"at deadline", func(i *secrets.Installation) { i.InstalledAt = record.Delivery.ExpiresAt }},
		{"after receipt", func(i *secrets.Installation) { i.InstalledAt = received.Add(time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := installation
			test.change(&changed)
			if _, err := record.Acknowledge(changed, received); err == nil {
				t.Fatal("invalid installation accepted")
			}
		})
	}
	for _, now := range []time.Time{time.Time{}, installation.InstalledAt.Add(-time.Nanosecond), record.Request.ExpiresAt, record.Request.ExpiresAt.Add(time.Hour)} {
		if _, err := record.Acknowledge(installation, now); err == nil {
			t.Fatal("invalid or expired receipt time accepted")
		}
	}
	fulfilled, err := record.Acknowledge(installation, received)
	if err != nil {
		t.Fatal(err)
	}
	if fulfilled.Request.Status != secrets.Fulfilled || !fulfilled.Request.ResolvedAt.Equal(received) ||
		fulfilled.Installation == nil || *fulfilled.Installation != installation ||
		fulfilled.Request.Fulfillment.DeliveryID != record.Delivery.ID {
		t.Fatal("acknowledgement did not save complete fulfillment evidence")
	}
	if record.Installation != nil || record.Request.Status != secrets.Pending {
		t.Fatal("acknowledgement changed its source record")
	}
	replayed, err := fulfilled.Acknowledge(installation, record.Request.ExpiresAt.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replayed, fulfilled) {
		t.Fatalf("late exact acknowledgement replay changed history: %v", err)
	}
	for _, change := range []func(*secrets.Installation){
		func(i *secrets.Installation) { i.ID = fixtures.DeliveryID },
		func(i *secrets.Installation) { i.InstalledAt = i.InstalledAt.Add(time.Nanosecond) },
		func(i *secrets.Installation) { i.SecretVersion++ },
	} {
		changed := installation
		change(&changed)
		if _, err := fulfilled.Acknowledge(changed, received.Add(time.Hour)); !errors.Is(err, secrets.ErrRequestConflict) {
			t.Fatalf("changed acknowledgement replaced historical evidence: %v", err)
		}
	}

	short := fixtures.Delivery(t)
	short.Secret.Metadata.ExpiresAt = fixtures.RequestedAt().Add(5 * time.Minute)
	short.ExpiresAt = short.Secret.Metadata.ExpiresAt
	shortRecord, err := fixtures.PendingRecord(t).WithDelivery(short)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shortRecord.Acknowledge(fixtures.Installation(short), short.ExpiresAt); !errors.Is(err, secrets.ErrRequestExpired) {
		t.Fatalf("expired secret delivery fulfilled an otherwise live request: %v", err)
	}
}

func TestRequestRecordTerminalStatesCannotReopen(t *testing.T) {
	for _, withDelivery := range []bool{false, true} {
		original := fixtures.PendingRecord(t)
		if withDelivery {
			original = fixtures.DeliveryRecord(t)
			if _, err := original.Cancel(original.Delivery.CreatedAt.Add(-time.Nanosecond)); err == nil {
				t.Fatal("request cancellation preceded its saved delivery")
			}
		}
		cancelled, err := original.Cancel(fixtures.RequestedAt().Add(3 * time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		expired, err := original.Expire(original.Request.ExpiresAt)
		if err != nil {
			t.Fatal(err)
		}
		if original.Request.Status != secrets.Pending {
			t.Fatal("terminal transition changed source record")
		}
		for _, terminal := range []secrets.RequestRecord{cancelled, expired} {
			if err := terminal.Validate(); err != nil {
				t.Fatal(err)
			}
			if withDelivery {
				replayed, err := terminal.WithDelivery(*terminal.Delivery)
				if err != nil || !reflect.DeepEqual(replayed, terminal) {
					t.Fatalf("late delivery retry changed terminal state: %v", err)
				}
				if _, err := terminal.Acknowledge(fixtures.Installation(*terminal.Delivery), fixtures.RequestedAt().Add(3*time.Minute)); !errors.Is(err, secrets.ErrRequestClosed) {
					t.Fatalf("acknowledgement reopened terminal request: %v", err)
				}
			} else if _, err := terminal.WithDelivery(fixtures.Delivery(t)); !errors.Is(err, secrets.ErrRequestClosed) {
				t.Fatalf("new delivery added to terminal request: %v", err)
			}
		}
		replayed, err := cancelled.Cancel(original.Request.ExpiresAt.Add(time.Hour))
		if err != nil || !reflect.DeepEqual(replayed, cancelled) {
			t.Fatalf("cancellation retry changed history: %v", err)
		}
		replayed, err = expired.Expire(original.Request.ExpiresAt.Add(time.Hour))
		if err != nil || !reflect.DeepEqual(replayed, expired) {
			t.Fatalf("expiration retry changed history: %v", err)
		}
	}
}

func TestRequestRecordSnapshotsAndSerializationKeepEvidenceIndependent(t *testing.T) {
	record := fixtures.DeliveryRecord(t)
	installation := fixtures.Installation(*record.Delivery)
	fulfilled, err := record.Acknowledge(installation, installation.InstalledAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	clone := fulfilled.Clone()
	clone.Delivery.Secret.Value.Ciphertext[0] ^= 1
	clone.Delivery.Secret.Value.Nonce[0] ^= 1
	clone.Request.Fulfillment.SecretVersion++
	clone.Installation.SecretVersion++
	if bytes.Equal(clone.Delivery.Secret.Value.Ciphertext, fulfilled.Delivery.Secret.Value.Ciphertext) ||
		bytes.Equal(clone.Delivery.Secret.Value.Nonce, fulfilled.Delivery.Secret.Value.Nonce) ||
		fulfilled.Request.Fulfillment.SecretVersion != installation.SecretVersion || fulfilled.Installation.SecretVersion != installation.SecretVersion {
		t.Fatal("cloned record aliases saved evidence")
	}
	encoded, err := json.Marshal(fulfilled)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("record-secret-plaintext-fixture")) || bytes.Contains(encoded, []byte(fixtures.EncryptionKey)) {
		t.Fatal("serialized request record exposed secret plaintext or encryption key")
	}
	var restored secrets.RequestRecord
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil || !reflect.DeepEqual(restored, fulfilled) {
		t.Fatalf("serialization changed request evidence: %v", err)
	}
	for _, mutate := range []func(*secrets.RequestRecord){
		func(r *secrets.RequestRecord) { r.Delivery = nil },
		func(r *secrets.RequestRecord) { r.Installation = nil },
		func(r *secrets.RequestRecord) { r.Installation.DeliveryID = fixtures.InstallationID },
		func(r *secrets.RequestRecord) { r.Request.Fulfillment.SecretVersion++ },
		func(r *secrets.RequestRecord) { r.Request.ResolvedAt = installation.InstalledAt.Add(-time.Nanosecond) },
	} {
		broken := fulfilled.Clone()
		mutate(&broken)
		if broken.Validate() == nil {
			t.Fatal("incomplete or conflicting fulfillment evidence accepted")
		}
	}
	pending := record.Clone()
	pending.Installation = &installation
	if pending.Validate() == nil {
		t.Fatal("unfulfilled request accepted installation evidence")
	}
}
