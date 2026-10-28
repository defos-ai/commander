package mongo_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretmongo "github.com/defos-ai/commander/providers/mongo/secrets"
	"github.com/defos-ai/commander/providers/mongo/tests/fixtures"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRequestMongoStoreEncryptedDeliveryAndRestart(t *testing.T) {
	database := fixtures.OpenStore(t, "secret_requests")
	requests := fixtures.OpenRequestStore(t, database)
	request, delivery, installation := fixtures.SecretRequest(t)
	items, err := requests.ListPending(t.Context())
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("empty pending requests: count=%d error=%v", len(items), err)
	}
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := requests.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	want, err := secrets.NewRequestRecord(request)
	if err != nil {
		t.Fatal(err)
	}
	want, err = want.WithDelivery(delivery)
	if err != nil {
		t.Fatal(err)
	}
	delivery.Secret.Value.Ciphertext[0] ^= 1
	delivery.Secret.Value.Nonce[0] ^= 1
	requests = fixtures.OpenRequestStore(t, database)
	saved := assertMongoRequest(t, requests, want)
	cipher, err := crypto.NewSecretCipher(fixtures.SecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(saved.Delivery.Secret)
	if err != nil || string(plaintext) != fixtures.SecretPlaintext {
		t.Fatal("reopened delivery lost encrypted value integrity")
	}
	saved.Delivery.Secret.Value.Ciphertext[0] ^= 1
	assertMongoRequest(t, requests, want)
	items, err = requests.ListPending(t.Context())
	if err != nil || !reflect.DeepEqual(items, []secrets.RequestRecord{want}) {
		t.Fatalf("pending inventory lost delivery: %v", err)
	}
	items[0].Delivery.Secret.Value.Nonce[0] ^= 1
	assertMongoRequest(t, requests, want)

	stale := installation
	stale.SecretVersion--
	if err := requests.Acknowledge(t.Context(), stale, installation.InstalledAt); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("stale installation accepted: %v", err)
	}
	assertMongoRequest(t, requests, want)
	if err := requests.Acknowledge(t.Context(), installation, installation.InstalledAt); err != nil {
		t.Fatal(err)
	}
	want, err = want.Acknowledge(installation, installation.InstalledAt)
	if err != nil {
		t.Fatal(err)
	}
	requests = fixtures.OpenRequestStore(t, database)
	assertMongoRequest(t, requests, want)
	for _, replay := range []func() error{
		func() error { return requests.Create(t.Context(), request) },
		func() error { return requests.SaveDelivery(t.Context(), *want.Delivery) },
		func() error { return requests.Acknowledge(t.Context(), installation, request.ExpiresAt.Add(time.Hour)) },
	} {
		if err := replay(); err != nil {
			t.Fatalf("exact replay after fulfillment: %v", err)
		}
		assertMongoRequest(t, requests, want)
	}
	items, err = requests.ListPending(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("fulfilled request remained pending: %v", err)
	}

	var documents []bson.Raw
	if err := database.Find(t.Context(), "secret_requests", bson.D{}, &documents); err != nil {
		t.Fatal(err)
	}
	keyBytes, _ := hex.DecodeString(fixtures.SecretEncryptionKey)
	if len(documents) != 1 {
		t.Fatal("expected one durable request aggregate")
	}
	for _, sensitive := range [][]byte{[]byte(fixtures.SecretPlaintext), []byte(fixtures.SecretEncryptionKey), keyBytes} {
		if bytes.Contains(documents[0], sensitive) {
			t.Fatal("request persistence exposed plaintext or encryption key material")
		}
	}
}

func TestRequestMongoStorePermanentIdentities(t *testing.T) {
	database := fixtures.OpenStore(t, "secret_requests")
	requests := fixtures.OpenRequestStore(t, database)
	request, delivery, _ := fixtures.SecretRequest(t)
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	conflicting := request
	conflicting.AgentID = "builder-agent-87654321"
	if err := requests.Create(t.Context(), conflicting); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("request ID changed identity: %v", err)
	}
	if err := requests.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	replacement := delivery
	replacement.ID = id.New()
	if err := requests.SaveDelivery(t.Context(), replacement); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("request accepted a second delivery: %v", err)
	}
	if err := requests.Cancel(t.Context(), request.ID, delivery.CreatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	other := request
	other.ID = id.New()
	if err := requests.Create(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	duplicateDelivery := delivery
	duplicateDelivery.RequestID = other.ID
	if err := requests.SaveDelivery(t.Context(), duplicateDelivery); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("terminal request released its delivery ID: %v", err)
	}
	saved, err := requests.Get(t.Context(), other.ID)
	if err != nil || saved.Delivery != nil || saved.Request.Status != secrets.Pending {
		t.Fatalf("delivery-ID conflict partially changed another request: %v", err)
	}
	if err := requests.Create(t.Context(), request); err != nil {
		t.Fatalf("original create replay after cancellation: %v", err)
	}
	saved, err = fixtures.OpenRequestStore(t, database).Get(t.Context(), request.ID)
	if err != nil || saved.Request.Status != secrets.Cancelled || saved.Delivery == nil || saved.Delivery.ID != delivery.ID {
		t.Fatalf("replay revived a cancelled request or removed delivery identity: %v", err)
	}
}

func TestRequestMongoStoreConcurrentReplaysAndSettlement(t *testing.T) {
	database := fixtures.OpenStore(t, "secret_requests")
	first, second := fixtures.OpenRequestStore(t, database), fixtures.OpenRequestStore(t, database)
	request, delivery, installation := fixtures.SecretRequest(t)
	for _, operations := range [][]func() error{
		{func() error { return first.Create(t.Context(), request) }, func() error { return second.Create(t.Context(), request) }},
		{func() error { return first.SaveDelivery(t.Context(), delivery) }, func() error { return second.SaveDelivery(t.Context(), delivery) }},
		{func() error { return first.Acknowledge(t.Context(), installation, installation.InstalledAt) },
			func() error { return second.Acknowledge(t.Context(), installation, installation.InstalledAt) }},
	} {
		for _, err := range raceRequestWrites(t, operations) {
			if err != nil {
				t.Fatalf("concurrent identical replay: %v", err)
			}
		}
	}
	saved, err := first.Get(t.Context(), request.ID)
	if err != nil || saved.Request.Status != secrets.Fulfilled || saved.Installation == nil || saved.Installation.ID != installation.ID {
		t.Fatalf("concurrent replays lost fulfillment: %v", err)
	}

	request, delivery, installation = fixtures.SecretRequest(t)
	if err := first.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := first.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	results := raceRequestWrites(t, []func() error{
		func() error { return first.Acknowledge(t.Context(), installation, installation.InstalledAt) },
		func() error { return second.Cancel(t.Context(), request.ID, installation.InstalledAt) },
	})
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, secrets.ErrRequestClosed) && !errors.Is(err, secrets.ErrRequestConflict) {
			t.Fatalf("unexpected concurrent settlement error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("competing settlements succeeded %d times", successes)
	}
	saved, err = first.Get(t.Context(), request.ID)
	if err != nil || saved.Validate() != nil || (saved.Request.Status != secrets.Fulfilled && saved.Request.Status != secrets.Cancelled) {
		t.Fatalf("race left inconsistent request: %v", err)
	}
}

func TestRequestMongoStoreExpirationInventoryAndCancellation(t *testing.T) {
	requests := fixtures.OpenRequestStore(t, fixtures.OpenStore(t, "secret_requests"))
	request, delivery, installation := fixtures.SecretRequest(t)
	other := request
	other.ID = id.New()
	for _, item := range []secrets.Request{request, other} {
		if err := requests.Create(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	if err := requests.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	items, err := requests.ListPending(t.Context())
	if err != nil || len(items) != 2 || !sort.SliceIsSorted(items, func(i, j int) bool { return items[i].Request.ID < items[j].Request.ID }) {
		t.Fatalf("pending inventory must include elapsed deadlines in ID order: count=%d error=%v", len(items), err)
	}
	if err := requests.Expire(t.Context(), request.ID, request.ExpiresAt.Add(-time.Nanosecond)); err == nil {
		t.Fatal("request expired early")
	}
	if err := requests.Acknowledge(t.Context(), installation, delivery.ExpiresAt); err == nil {
		t.Fatal("expired delivery acknowledged")
	}
	if err := requests.Expire(t.Context(), request.ID, request.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	expired, err := requests.Get(t.Context(), request.ID)
	if err != nil || expired.Request.Status != secrets.RequestExpired {
		t.Fatalf("expiry was not persisted: %v", err)
	}
	if err := requests.Expire(t.Context(), request.ID, request.ExpiresAt.Add(time.Hour)); err != nil {
		t.Fatalf("expiry replay: %v", err)
	}
	assertMongoRequest(t, requests, expired)
	if err := requests.Acknowledge(t.Context(), installation, installation.InstalledAt); err == nil {
		t.Fatal("late acknowledgement revived expired request")
	}
	if err := requests.Cancel(t.Context(), other.ID, other.RequestedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err = requests.ListPending(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("terminal requests remained pending: %v", err)
	}
	if _, err := requests.Get(t.Context(), id.New()); !errors.Is(err, secrets.ErrUnknownRequest) {
		t.Fatalf("unknown request lookup: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, call := range []func() error{
		func() error { return requests.Create(ctx, request) },
		func() error { _, err := requests.Get(ctx, request.ID); return err },
		func() error { _, err := requests.ListPending(ctx); return err },
		func() error { return requests.SaveDelivery(ctx, delivery) },
		func() error { return requests.Acknowledge(ctx, installation, installation.InstalledAt) },
		func() error { return requests.Cancel(ctx, request.ID, installation.InstalledAt) },
		func() error { return requests.Expire(ctx, request.ID, request.ExpiresAt) },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled operation: %v", err)
		}
	}
}

func assertMongoRequest(t *testing.T, requests *secretmongo.RequestMongoStore, want secrets.RequestRecord) secrets.RequestRecord {
	t.Helper()
	saved, err := requests.Get(t.Context(), want.Request.ID)
	if err != nil || !reflect.DeepEqual(saved, want) {
		t.Fatalf("saved request changed its metadata, delivery or installation: %v", err)
	}
	return saved
}

func raceRequestWrites(t *testing.T, calls []func() error) []error {
	t.Helper()
	start, results := make(chan struct{}), make(chan error, len(calls))
	for _, call := range calls {
		go func() { <-start; results <- call() }()
	}
	close(start)
	errors := make([]error, len(calls))
	for i := range errors {
		select {
		case errors[i] = <-results:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent request writes did not finish")
		}
	}
	return errors
}
