package secrets_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
)

const installationID = "00000000-0000-4000-8000-000000000006"

func pendingRecord(t *testing.T) secrets.RequestRecord {
	t.Helper()
	record, err := secrets.NewRequestRecord(requestFixture())
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func deliveryFixture(t *testing.T) secrets.Delivery {
	t.Helper()
	secret, err := newSecretCipher(t).Encrypt(secretFixture().Metadata, []byte("record-secret-plaintext-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	request := requestFixture()
	return secrets.Delivery{ID: deliveryID, RequestID: request.ID, AgentID: request.AgentID,
		Secret: secret, CreatedAt: requestedAt.Add(time.Minute), ExpiresAt: request.ExpiresAt}
}

func deliveryRecord(t *testing.T) secrets.RequestRecord {
	t.Helper()
	record, err := pendingRecord(t).WithDelivery(deliveryFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func installationFixture(delivery secrets.Delivery) secrets.Installation {
	return secrets.Installation{ID: installationID, RequestID: delivery.RequestID,
		DeliveryID: delivery.ID, AgentID: delivery.AgentID, SecretID: delivery.Secret.Metadata.ID,
		SecretVersion: delivery.Secret.Metadata.Version, InstalledAt: delivery.CreatedAt.Add(time.Minute)}
}

func TestRequestRecordRequiresPendingCreationAndPreservesOriginalIdentity(t *testing.T) {
	request := requestFixture()
	record := pendingRecord(t)
	if !record.MatchesRequest(request) || record.Delivery != nil || record.Installation != nil {
		t.Fatal("new request record did not retain the pending identity")
	}
	cancelled, err := request.Cancel(requestedAt.Add(time.Minute))
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
		func(r *secrets.Request) { r.ID = installationID },
		func(r *secrets.Request) { r.AgentID = "builder-agent-87654321" },
		func(r *secrets.Request) { r.Key.Scope.AgentType = "another-agent" },
		func(r *secrets.Request) { r.Key.Name = "OTHER_TOKEN" },
		func(r *secrets.Request) { r.OperationID = installationID },
		func(r *secrets.Request) { r.RequestedAt = r.RequestedAt.Add(-time.Second) },
		func(r *secrets.Request) { r.ExpiresAt = r.ExpiresAt.Add(time.Second) },
	} {
		changed := request.Clone()
		mutate(&changed)
		if record.MatchesRequest(changed) {
			t.Fatal("changed request matched the original identity")
		}
	}
	withDelivery := deliveryRecord(t)
	ack := installationFixture(*withDelivery.Delivery)
	fulfilled, err := withDelivery.Acknowledge(ack, ack.InstalledAt.Add(time.Second))
	if err != nil || !fulfilled.MatchesRequest(request) {
		t.Fatalf("progress changed original request matching: %v", err)
	}
}

func TestRequestRecordDeliveryIsImmutableAndBoundToRequest(t *testing.T) {
	pending := pendingRecord(t)
	delivery := deliveryFixture(t)
	for _, test := range []struct {
		name   string
		change func(*secrets.Delivery)
	}{
		{"request", func(d *secrets.Delivery) { d.RequestID = installationID }},
		{"recipient", func(d *secrets.Delivery) { d.AgentID = "builder-agent-87654321" }},
		{"agent type", func(d *secrets.Delivery) { d.Secret.Metadata.Key.Scope.AgentType = "other-agent" }},
		{"name", func(d *secrets.Delivery) { d.Secret.Metadata.Key.Name = "OTHER_TOKEN" }},
		{"before request", func(d *secrets.Delivery) { d.CreatedAt = requestedAt.Add(-time.Second) }},
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
		func(d *secrets.Delivery) { d.ID = installationID },
		func(d *secrets.Delivery) { d.Secret.Metadata.Version++ },
		func(d *secrets.Delivery) { d.Secret.Metadata.ID = installationID },
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

	short := deliveryFixture(t)
	short.Secret.Metadata.ExpiresAt = requestedAt.Add(5 * time.Minute)
	short.ExpiresAt = short.Secret.Metadata.ExpiresAt
	if _, err := pending.WithDelivery(short); err != nil {
		t.Fatalf("secret expiration could not bound delivery deadline: %v", err)
	}
}

func TestRequestRecordAcknowledgementRequiresExactEvidenceAndReceiptTime(t *testing.T) {
	record := deliveryRecord(t)
	installation := installationFixture(*record.Delivery)
	received := installation.InstalledAt.Add(time.Second)
	if _, err := pendingRecord(t).Acknowledge(installation, received); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("acknowledgement without saved delivery accepted: %v", err)
	}
	for _, test := range []struct {
		name   string
		change func(*secrets.Installation)
	}{
		{"request", func(i *secrets.Installation) { i.RequestID = deliveryID }},
		{"delivery", func(i *secrets.Installation) { i.DeliveryID = installationID }},
		{"recipient", func(i *secrets.Installation) { i.AgentID = "builder-agent-87654321" }},
		{"secret identity", func(i *secrets.Installation) { i.SecretID = installationID }},
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
		func(i *secrets.Installation) { i.ID = deliveryID },
		func(i *secrets.Installation) { i.InstalledAt = i.InstalledAt.Add(time.Nanosecond) },
		func(i *secrets.Installation) { i.SecretVersion++ },
	} {
		changed := installation
		change(&changed)
		if _, err := fulfilled.Acknowledge(changed, received.Add(time.Hour)); !errors.Is(err, secrets.ErrRequestConflict) {
			t.Fatalf("changed acknowledgement replaced historical evidence: %v", err)
		}
	}

	short := deliveryFixture(t)
	short.Secret.Metadata.ExpiresAt = requestedAt.Add(5 * time.Minute)
	short.ExpiresAt = short.Secret.Metadata.ExpiresAt
	shortRecord, err := pendingRecord(t).WithDelivery(short)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := shortRecord.Acknowledge(installationFixture(short), short.ExpiresAt); !errors.Is(err, secrets.ErrRequestExpired) {
		t.Fatalf("expired secret delivery fulfilled an otherwise live request: %v", err)
	}
}

func TestRequestRecordTerminalStatesCannotReopen(t *testing.T) {
	for _, withDelivery := range []bool{false, true} {
		original := pendingRecord(t)
		if withDelivery {
			original = deliveryRecord(t)
			if _, err := original.Cancel(original.Delivery.CreatedAt.Add(-time.Nanosecond)); err == nil {
				t.Fatal("request cancellation preceded its saved delivery")
			}
		}
		cancelled, err := original.Cancel(requestedAt.Add(3 * time.Minute))
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
				if _, err := terminal.Acknowledge(installationFixture(*terminal.Delivery), requestedAt.Add(3*time.Minute)); !errors.Is(err, secrets.ErrRequestClosed) {
					t.Fatalf("acknowledgement reopened terminal request: %v", err)
				}
			} else if _, err := terminal.WithDelivery(deliveryFixture(t)); !errors.Is(err, secrets.ErrRequestClosed) {
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
	record := deliveryRecord(t)
	installation := installationFixture(*record.Delivery)
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
	if bytes.Contains(encoded, []byte("record-secret-plaintext-fixture")) || bytes.Contains(encoded, []byte(encryptionKey)) {
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
		func(r *secrets.RequestRecord) { r.Installation.DeliveryID = installationID },
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
