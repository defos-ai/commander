package secrets

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	commandercrypto "github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestSecretDocumentPreservesAuthenticatedValues(t *testing.T) {
	for _, test := range []struct {
		name    string
		version uint64
	}{
		{"version-one", 1},
		{"above-int64", math.MaxInt64 + 1},
		{"max-uint64", math.MaxUint64},
	} {
		t.Run(test.name, func(t *testing.T) {
			cipher, secret := encryptedDocumentFixture(t, test.version)
			document, err := encode(secret)
			if err != nil {
				t.Fatal(err)
			}
			data, err := bson.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if bson.Raw(data).Lookup("data").Type != bson.TypeBinary {
				t.Fatal("encrypted JSON payload must be BSON binary")
			}
			var saved secretDocument
			if err := bson.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			actual, err := saved.decode()
			if err != nil {
				t.Fatal(err)
			}
			if actual.Metadata.Version != test.version || !equalSecrets(actual, secret) {
				t.Fatal("BSON roundtrip changed the authenticated record")
			}
			plaintext, err := cipher.Decrypt(actual)
			if err != nil || !bytes.Equal(plaintext, []byte("test-only-secret-value")) {
				t.Fatal("roundtripped record did not authenticate and decrypt")
			}
			// Timezone formatting is not identity: these describe the same instants.
			actual.Metadata.CreatedAt = actual.Metadata.CreatedAt.UTC()
			actual.Metadata.UpdatedAt = actual.Metadata.UpdatedAt.UTC()
			actual.Metadata.ExpiresAt = actual.Metadata.ExpiresAt.UTC()
			if !equalSecrets(actual, secret) {
				t.Fatal("equivalent timestamp offsets must be accepted as an identical replay")
			}
		})
	}
}

func TestSecretDocumentRejectsInvalidStoredRecords(t *testing.T) {
	_, secret := encryptedDocumentFixture(t, 1)
	valid, err := encode(secret)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		change func(*secretDocument)
	}{
		{"indexed ID mismatch", func(doc *secretDocument) { doc.ID = "65abdb41-d282-4508-940e-8c01ca480487" }},
		{"indexed agent type mismatch", func(doc *secretDocument) { doc.AgentType = "another-agent" }},
		{"indexed profile mismatch", func(doc *secretDocument) { doc.Profile = "another-profile" }},
		{"indexed name mismatch", func(doc *secretDocument) { doc.Name = "OTHER_TOKEN" }},
		{"active missing payload", func(doc *secretDocument) { doc.Data = nil }},
		{"malformed JSON", func(doc *secretDocument) { doc.Data = []byte(`{"metadata":`) }},
		{"unknown field", func(doc *secretDocument) { doc.Data = []byte(`{"unexpected-secret-value":"do-not-disclose"}`) }},
		{"multiple JSON values", func(doc *secretDocument) { doc.Data = append(doc.Data, []byte(` {}`)...) }},
		{"retired with payload", func(doc *secretDocument) { doc.Active = false }},
		{"retired invalid ID", func(doc *secretDocument) { doc.Active, doc.Data, doc.ID = false, nil, "invalid" }},
		{"retired invalid key", func(doc *secretDocument) { doc.Active, doc.Data, doc.Profile = false, nil, "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := valid
			doc.Data = bytes.Clone(valid.Data)
			test.change(&doc)
			_, err := doc.decode()
			if err == nil {
				t.Fatal("invalid stored record was accepted")
			}
			if strings.Contains(err.Error(), "unexpected-secret-value") || strings.Contains(err.Error(), "do-not-disclose") {
				t.Fatal("decoding error disclosed stored values")
			}
		})
	}
	retired := identityDocument(secret.Metadata.ID, secret.Metadata.Key)
	if _, err := retired.decode(); err != nil {
		t.Fatalf("valid retired identity: %v", err)
	}
	data, err := bson.Marshal(retired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bson.Raw(data).LookupErr("data"); err == nil {
		t.Fatal("retired identity still contains an encrypted payload")
	}
}

func TestSecretWriteErrorPreservesOutcomeAndCause(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("connection lost")} {
		if got := writeError(false, cause); got != cause {
			t.Fatal("uncommitted write changed its original error")
		}
		got := writeError(true, cause)
		if !errors.Is(got, secretmodel.ErrWriteUncertain) || !errors.Is(got, cause) {
			t.Fatal("uncertain write must retain both the domain marker and cause")
		}
	}
	for _, wrote := range []bool{false, true} {
		if err := writeError(wrote, nil); err != nil {
			t.Fatal("successful write was marked uncertain")
		}
	}
	cause := mongo.CommandError{Code: 91, Message: "shutdown", Labels: []string{"UnknownTransactionCommitResult"}}
	var commandError mongo.CommandError
	if err := writeError(true, cause); !errors.As(err, &commandError) || !commandError.HasErrorLabel("UnknownTransactionCommitResult") {
		t.Fatal("uncertain write lost the driver's commit-result label")
	}
}

func encryptedDocumentFixture(t *testing.T, version uint64) (*commandercrypto.SecretCipher, secretmodel.Secret) {
	t.Helper()
	cipher, err := commandercrypto.NewSecretCipher(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, time.October, 16, 12, 34, 56, 123456789, time.FixedZone("fixture", 3*60*60))
	metadata := secretmodel.Metadata{
		ID:      "6c3e42ae-ebd7-4a76-a8f0-10229d05df62",
		Key:     secretmodel.Key{Scope: secretmodel.Scope{AgentType: "builder-agent", Profile: "default"}, Name: "TOKEN"},
		Version: version, CreatedAt: created, UpdatedAt: created.Add(113 * time.Nanosecond), ExpiresAt: created.Add(29*time.Minute + 71*time.Nanosecond),
	}
	secret, err := cipher.Encrypt(metadata, []byte("test-only-secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	return cipher, secret
}
