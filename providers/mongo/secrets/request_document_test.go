package secrets

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRequestMongoStoreRequiresDatabaseAndContext(t *testing.T) {
	if _, err := NewRequestMongoStore(t.Context(), nil); err == nil {
		t.Fatal("constructor accepted a missing database")
	}
	if _, err := NewRequestMongoStore(nil, nil); err == nil {
		t.Fatal("constructor accepted a missing context")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewRequestMongoStore(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor lost cancellation: %v", err)
	}
}

func TestRequestDocumentPreservesAuthenticatedDelivery(t *testing.T) {
	cipher, secret := encryptedDocumentFixture(t, math.MaxUint64)
	stamp := secret.Metadata.UpdatedAt.Add(time.Second)
	request := secretmodel.Request{ID: id.New(), Key: secret.Metadata.Key, AgentID: "builder-agent-12345678", AgentType: "builder-agent",
		Status: secretmodel.Pending, RequestedAt: stamp, ExpiresAt: stamp.Add(time.Hour)}
	record, err := secretmodel.NewRequestRecord(request)
	if err != nil {
		t.Fatal(err)
	}
	record, err = record.WithDelivery(secretmodel.Delivery{ID: id.New(), RequestID: request.ID, AgentID: request.AgentID,
		Secret: secret, CreatedAt: stamp.Add(time.Nanosecond), ExpiresAt: secret.Metadata.ExpiresAt})
	if err != nil {
		t.Fatal(err)
	}
	document, err := encodeRequest(record)
	if err != nil {
		t.Fatal(err)
	}
	data, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if bson.Raw(data).Lookup("data").Type != bson.TypeBinary {
		t.Fatal("request aggregate must preserve JSON as BSON binary")
	}
	var saved requestDocument
	if err := bson.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	actual, err := saved.decode()
	if err != nil {
		t.Fatal(err)
	}
	if actual.Delivery.Secret.Metadata.Version != math.MaxUint64 ||
		!actual.Request.RequestedAt.Equal(record.Request.RequestedAt) || !actual.Delivery.CreatedAt.Equal(record.Delivery.CreatedAt) {
		t.Fatal("request document lost timestamps or full uint64 version")
	}
	plaintext, err := cipher.Decrypt(actual.Delivery.Secret)
	if err != nil || !bytes.Equal(plaintext, []byte("test-only-secret-value")) {
		t.Fatal("request document lost encrypted delivery integrity")
	}
	for _, test := range []struct {
		name   string
		change func(*requestDocument)
	}{
		{"indexed ID", func(doc *requestDocument) { doc.ID = id.New() }},
		{"indexed status", func(doc *requestDocument) { doc.Status = secretmodel.Cancelled }},
		{"indexed delivery", func(doc *requestDocument) { doc.DeliveryID = id.New() }},
		{"missing payload", func(doc *requestDocument) { doc.Data = nil }},
		{"unknown payload field", func(doc *requestDocument) { doc.Data = []byte(`{"private-test-value":"do-not-disclose"}`) }},
		{"multiple values", func(doc *requestDocument) { doc.Data = append(doc.Data, []byte(` {}`)...) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := document
			invalid.Data = bytes.Clone(document.Data)
			test.change(&invalid)
			if _, err := invalid.decode(); err == nil {
				t.Fatal("invalid request document accepted")
			} else if strings.Contains(err.Error(), "private-test-value") || strings.Contains(err.Error(), "do-not-disclose") {
				t.Fatal("request decoding exposed payload values")
			}
		})
	}
}
