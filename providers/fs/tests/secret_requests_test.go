package providers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretfs "github.com/defos-ai/commander/providers/fs/secrets"
)

func TestRequestFsStorePersistsDeliveryAndExactAcknowledgement(t *testing.T) {
	root := t.TempDir()
	store := openRequestFsStore(t, root)
	request, delivery, installation := fsRequestFixture(t)
	if err := store.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	expected := delivery
	expected.Secret = delivery.Secret.Clone()
	delivery.Secret.Value.Nonce[0] ^= 1
	delivery.Secret.Value.Ciphertext[0] ^= 1
	store = openRequestFsStore(t, root)
	record, err := store.Get(t.Context(), request.ID)
	if err != nil || record.Delivery == nil || !reflect.DeepEqual(*record.Delivery, expected) || record.Installation != nil || record.Request.Status != secrets.Pending {
		t.Fatalf("reopened store lost pending request or encrypted delivery: %v", err)
	}
	cipher, err := crypto.NewSecretCipher(fsSecretEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(record.Delivery.Secret)
	if err != nil || string(plaintext) != fsSecretPlaintext {
		t.Fatal("stored delivery lost authenticated metadata or encrypted bytes")
	}
	record.Delivery.Secret.Value.Ciphertext[0] ^= 1
	if err := store.Create(t.Context(), request); err != nil {
		t.Fatal("initial request replay must preserve its delivery")
	}
	now := installation.InstalledAt.Add(time.Second)
	if err := store.Acknowledge(t.Context(), installation, now); err != nil {
		t.Fatal(err)
	}
	for _, replay := range []func() error{
		func() error { return store.Create(t.Context(), request) },
		func() error { return store.SaveDelivery(t.Context(), expected) },
		func() error { return store.Acknowledge(t.Context(), installation, request.ExpiresAt.Add(time.Hour)) },
	} {
		if err := replay(); err != nil {
			t.Fatalf("exact replay after fulfillment failed: %v", err)
		}
	}
	reopened := openRequestFsStore(t, root)
	saved, err := reopened.Get(t.Context(), request.ID)
	if err != nil || saved.Request.Status != secrets.Fulfilled || !saved.Request.ResolvedAt.Equal(now) || saved.Installation == nil || !reflect.DeepEqual(*saved.Installation, installation) || saved.Delivery == nil || !reflect.DeepEqual(*saved.Delivery, expected) {
		t.Fatal("retries changed the accepted delivery, acknowledgement or resolution time")
	}
	saved.Installation.ID = id.New()
	saved.Request.Fulfillment.SecretVersion++
	stillSaved, err := reopened.Get(t.Context(), request.ID)
	if err != nil || stillSaved.Installation.ID != installation.ID || stillSaved.Request.Fulfillment.SecretVersion != installation.SecretVersion {
		t.Fatal("returned request state aliases persisted acknowledgement")
	}
	pending, err := reopened.ListPending(t.Context())
	if err != nil || pending == nil || len(pending) != 0 {
		t.Fatal("fulfilled request remained pending")
	}
	data, err := os.ReadFile(filepath.Join(root, "secret-requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(fsSecretPlaintext)) || bytes.Contains(data, []byte(fsSecretEncryptionKey)) {
		t.Fatal("request store persisted plaintext or encryption key")
	}
	if _, err := os.Stat(filepath.Join(root, "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("request store opened the current secret database")
	}
}

func TestRequestFsStoreListsPendingAndSettlesExplicitly(t *testing.T) {
	store := openRequestFsStore(t, t.TempDir())
	requests := make([]secrets.Request, 3)
	for index := range requests {
		request, _, _ := fsRequestFixture(t)
		requests[index] = request
		if err := store.Create(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	list, err := store.ListPending(t.Context())
	if err != nil || len(list) != 3 {
		t.Fatal("pending inventory omitted requests whose deadlines elapsed")
	}
	if !sort.SliceIsSorted(list, func(i, j int) bool { return list[i].Request.ID < list[j].Request.ID }) {
		t.Fatal("pending requests are not ordered by request ID")
	}
	list[0].Request.Key.Name = "MUTATED"
	first, err := store.Get(t.Context(), list[0].Request.ID)
	if err != nil || first.Request.Key.Name == "MUTATED" {
		t.Fatal("pending inventory aliases persisted requests")
	}
	if err := store.Expire(t.Context(), requests[0].ID, requests[0].ExpiresAt.Add(-time.Nanosecond)); err == nil {
		t.Fatal("request expired before its deadline")
	}
	if err := store.Expire(t.Context(), requests[0].ID, requests[0].ExpiresAt); err != nil {
		t.Fatal(err)
	}
	cancelledAt := requests[1].RequestedAt.Add(time.Second)
	if err := store.Cancel(t.Context(), requests[1].ID, cancelledAt); err != nil {
		t.Fatal(err)
	}
	if err := store.Cancel(t.Context(), requests[1].ID, requests[1].ExpiresAt.Add(time.Hour)); err != nil {
		t.Fatal("exact cancellation replay failed")
	}
	if err := store.Expire(t.Context(), requests[0].ID, requests[0].ExpiresAt.Add(time.Hour)); err != nil {
		t.Fatal("exact expiration replay failed")
	}
	list, err = store.ListPending(t.Context())
	if err != nil || len(list) != 1 || list[0].Request.ID != requests[2].ID {
		t.Fatal("explicit settlement removed or retained the wrong requests")
	}
	for index, expected := range []secrets.RequestStatus{secrets.RequestExpired, secrets.Cancelled} {
		if err := store.Create(t.Context(), requests[index]); err != nil {
			t.Fatal("initial request replay failed after settlement")
		}
		record, err := store.Get(t.Context(), requests[index].ID)
		if err != nil || record.Request.Status != expected {
			t.Fatal("initial request replay reopened settled request")
		}
	}
	record, err := store.Get(t.Context(), requests[1].ID)
	if err != nil || !record.Request.ResolvedAt.Equal(cancelledAt) {
		t.Fatal("cancellation replay changed the resolution time")
	}
}

func TestRequestFsStoreRejectsConflictingIdentitiesAndReceipts(t *testing.T) {
	store := openRequestFsStore(t, t.TempDir())
	request, delivery, installation := fsRequestFixture(t)
	if err := store.Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	changedRequest := request.Clone()
	changedRequest.Key.Name = "OTHER_TOKEN"
	if err := store.Create(t.Context(), changedRequest); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("request identity reassigned to another key: %v", err)
	}
	if err := store.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*secrets.Delivery){
		func(d *secrets.Delivery) { d.ID = id.New() },
		func(d *secrets.Delivery) { d.Secret.Value.Ciphertext[0] ^= 1 },
		func(d *secrets.Delivery) { d.Secret.Metadata.ID = id.New() },
	} {
		changed := delivery
		changed.Secret = delivery.Secret.Clone()
		edit(&changed)
		if err := store.SaveDelivery(t.Context(), changed); !errors.Is(err, secrets.ErrRequestConflict) {
			t.Fatalf("immutable delivery changed: %v", err)
		}
	}
	otherRequest := request.Clone()
	otherRequest.ID = id.New()
	if err := store.Create(t.Context(), otherRequest); err != nil {
		t.Fatal(err)
	}
	otherDelivery := delivery
	otherDelivery.RequestID = otherRequest.ID
	if err := store.SaveDelivery(t.Context(), otherDelivery); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("delivery ID used by two requests: %v", err)
	}
	for _, edit := range []func(*secrets.Installation){
		func(i *secrets.Installation) { i.DeliveryID = id.New() },
		func(i *secrets.Installation) { i.SecretID = id.New() },
		func(i *secrets.Installation) { i.SecretVersion++ },
		func(i *secrets.Installation) { i.AgentID = "builder-agent-87654321" },
	} {
		changed := installation
		edit(&changed)
		if err := store.Acknowledge(t.Context(), changed, installation.InstalledAt.Add(time.Second)); err == nil {
			t.Fatal("mismatched installation was accepted")
		}
	}
	if err := store.Acknowledge(t.Context(), installation, request.ExpiresAt); err == nil {
		t.Fatal("new acknowledgement was accepted after the request deadline")
	}
	if err := store.Acknowledge(t.Context(), installation, installation.InstalledAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	changedAck := installation
	changedAck.ID = id.New()
	if err := store.Acknowledge(t.Context(), changedAck, installation.InstalledAt.Add(2*time.Second)); !errors.Is(err, secrets.ErrRequestConflict) {
		t.Fatalf("second acknowledgement replaced the original identity: %v", err)
	}
}

func TestRequestFsStoreContextAndUnknownRecords(t *testing.T) {
	store := openRequestFsStore(t, t.TempDir())
	request, delivery, installation := fsRequestFixture(t)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, cancelled} {
		for _, call := range []func() error{
			func() error { return store.Create(ctx, request) },
			func() error { _, err := store.Get(ctx, request.ID); return err },
			func() error { _, err := store.ListPending(ctx); return err },
			func() error { return store.SaveDelivery(ctx, delivery) },
			func() error { return store.Acknowledge(ctx, installation, installation.InstalledAt) },
			func() error { return store.Cancel(ctx, request.ID, request.RequestedAt) },
			func() error { return store.Expire(ctx, request.ID, request.ExpiresAt) },
		} {
			if err := call(); err == nil || (ctx != nil && !errors.Is(err, context.Canceled)) {
				t.Fatalf("invalid context was accepted or cancellation lost: %v", err)
			}
		}
	}
	for _, call := range []func() error{
		func() error { _, err := store.Get(t.Context(), request.ID); return err },
		func() error { return store.SaveDelivery(t.Context(), delivery) },
		func() error { return store.Acknowledge(t.Context(), installation, installation.InstalledAt) },
		func() error { return store.Cancel(t.Context(), request.ID, request.RequestedAt) },
		func() error { return store.Expire(t.Context(), request.ID, request.ExpiresAt) },
	} {
		if err := call(); !errors.Is(err, secrets.ErrUnknownRequest) {
			t.Fatalf("missing request was not reported: %v", err)
		}
	}
	list, err := store.ListPending(t.Context())
	if err != nil || list == nil || len(list) != 0 {
		t.Fatal("rejected operations created request state")
	}
}

func TestRequestFsStoreSerializesConcurrentDeliveryAndSettlement(t *testing.T) {
	root := t.TempDir()
	const count = 8
	stores := make([]*secretfs.RequestFsStore, count)
	for i := range stores {
		stores[i] = openRequestFsStore(t, root)
	}
	request, original, _ := fsRequestFixture(t)
	if err := stores[0].Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, count)
	var group sync.WaitGroup
	for _, store := range stores {
		delivery := original
		delivery.ID = id.New()
		group.Go(func() {
			<-start
			results <- store.SaveDelivery(t.Context(), delivery)
		})
	}
	close(start)
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, secrets.ErrRequestConflict) {
			t.Fatalf("unexpected concurrent delivery error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("competing deliveries had %d winners, want one", winners)
	}
	saved, err := stores[0].Get(t.Context(), request.ID)
	if err != nil || saved.Delivery == nil {
		t.Fatal("winning delivery was not saved")
	}
	installation := fsInstallation(*saved.Delivery)
	start = make(chan struct{})
	results = make(chan error, 2)
	group.Go(func() { <-start; results <- stores[0].Acknowledge(t.Context(), installation, installation.InstalledAt) })
	group.Go(func() { <-start; results <- stores[1].Cancel(t.Context(), request.ID, installation.InstalledAt) })
	close(start)
	group.Wait()
	close(results)
	winners = 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("competing settlement transitions both committed or both failed")
	}
	settled, err := openRequestFsStore(t, root).Get(t.Context(), request.ID)
	if err != nil || (settled.Request.Status != secrets.Fulfilled && settled.Request.Status != secrets.Cancelled) || (settled.Request.Status == secrets.Fulfilled) != (settled.Installation != nil) {
		t.Fatal("concurrent settlement left inconsistent request evidence")
	}
}

func TestRequestFsStoreRejectsCorruptSnapshots(t *testing.T) {
	request, delivery, _ := fsRequestFixture(t)
	record := secrets.RequestRecord{Request: request, Delivery: &delivery}
	second := record.Clone()
	second.Request.ID = id.New()
	second.Delivery.RequestID = second.Request.ID
	encode := func(value any) []byte {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, data := range [][]byte{
		[]byte(`[{`), []byte(`null`), []byte(`[{}]`),
		encode([]secrets.RequestRecord{record, record}),
		encode([]secrets.RequestRecord{record, second}),
		[]byte(`[{"unexpected-private-field":"private-stored-value"}]`),
	} {
		root := t.TempDir()
		store := openRequestFsStore(t, root)
		path := filepath.Join(root, "secret-requests.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := secretfs.NewRequestFsStore(root); err == nil || strings.Contains(err.Error(), "private-stored-value") || strings.Contains(err.Error(), "unexpected-private-field") {
			t.Fatal("constructor accepted corrupt state or exposed stored data")
		}
		for _, call := range []func() error{
			func() error { _, err := store.Get(t.Context(), request.ID); return err },
			func() error { _, err := store.ListPending(t.Context()); return err },
			func() error { return store.Create(t.Context(), request) },
			func() error { return store.SaveDelivery(t.Context(), delivery) },
			func() error { return store.Cancel(t.Context(), request.ID, request.RequestedAt) },
		} {
			if err := call(); err == nil {
				t.Fatal("operation accepted corrupt request state")
			}
			assertSecretFileUnchanged(t, path, data)
		}
	}
}

func openRequestFsStore(t *testing.T, root string) *secretfs.RequestFsStore {
	t.Helper()
	store, err := secretfs.NewRequestFsStore(root)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func fsRequestFixture(t *testing.T) (secrets.Request, secrets.Delivery, secrets.Installation) {
	t.Helper()
	secret := fsSecretFixture(t, "API_TOKEN")
	request := secrets.Request{
		ID: id.New(), Key: secret.Metadata.Key, AgentID: "builder-agent-12345678", Status: secrets.Pending,
		RequestedAt: secret.Metadata.CreatedAt, ExpiresAt: secret.Metadata.CreatedAt.Add(time.Hour),
	}
	delivery := secrets.Delivery{
		ID: id.New(), RequestID: request.ID, AgentID: request.AgentID, Secret: secret,
		CreatedAt: request.RequestedAt.Add(time.Second), ExpiresAt: request.ExpiresAt,
	}
	return request, delivery, fsInstallation(delivery)
}

func fsInstallation(delivery secrets.Delivery) secrets.Installation {
	return secrets.Installation{
		ID: id.New(), RequestID: delivery.RequestID, DeliveryID: delivery.ID, AgentID: delivery.AgentID,
		SecretID: delivery.Secret.Metadata.ID, SecretVersion: delivery.Secret.Metadata.Version,
		InstalledAt: delivery.CreatedAt.Add(time.Second),
	}
}
