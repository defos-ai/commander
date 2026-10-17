package secrets_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/crypto"
	"github.com/defos-ai/commander/framework/domain/secrets"
)

const encryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newSecretCipher(t *testing.T) *crypto.SecretCipher {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(encryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestEncryptionKeyConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, key string
		want      error
	}{
		{"missing", "", crypto.ErrMissingKey},
		{"short", encryptionKey[:62], crypto.ErrInvalidKey},
		{"long", encryptionKey + "00", crypto.ErrInvalidKey},
		{"nonhex", strings.Repeat("z", 64), crypto.ErrInvalidKey},
		{"whitespace", " " + encryptionKey, crypto.ErrInvalidKey},
	} {
		t.Run(test.name, func(t *testing.T) {
			cipher, err := crypto.NewSecretCipher(test.key)
			if cipher != nil || !errors.Is(err, test.want) {
				t.Fatalf("invalid key returned cipher=%v, error=%v", cipher != nil, err)
			}
			if test.key != "" && strings.Contains(err.Error(), test.key) {
				t.Fatal("configuration error disclosed key input")
			}
		})
	}

	lower := newSecretCipher(t)
	upper, err := crypto.NewSecretCipher(strings.ToUpper(encryptionKey))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := lower.Encrypt(secretFixture().Metadata, []byte("sample value"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := upper.Decrypt(secret)
	if err != nil || string(plaintext) != "sample value" {
		t.Fatalf("equivalent hex keys could not decrypt: %v", err)
	}
}

func TestSecretEncryptionRoundTripAndIsolation(t *testing.T) {
	cipher := newSecretCipher(t)
	for _, plaintext := range [][]byte{nil, {}, []byte("test secret"), {0, 255, 10, 128}} {
		expected := bytes.Clone(plaintext)
		secret, err := cipher.Encrypt(secretFixture().Metadata, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		if err := secret.Validate(); err != nil {
			t.Fatalf("encrypted record is invalid: %v", err)
		}
		if secret.Value.Format != crypto.Format || len(secret.Value.Nonce) != 12 {
			t.Fatal("encryption did not produce the supported envelope")
		}
		if !bytes.Equal(plaintext, expected) {
			t.Fatal("encryption modified the caller's plaintext")
		}
		if len(plaintext) > 0 {
			plaintext[0] ^= 0xff
		}
		decrypted, err := cipher.Decrypt(secret)
		if err != nil || !bytes.Equal(decrypted, expected) {
			t.Fatalf("round trip changed plaintext: %v", err)
		}
		if len(decrypted) > 0 {
			decrypted[0] ^= 0xff
		}
		again, err := cipher.Decrypt(secret)
		if err != nil || !bytes.Equal(again, expected) {
			t.Fatalf("changing plaintext changed the stored envelope: %v", err)
		}
	}

	first, err := cipher.Encrypt(secretFixture().Metadata, []byte("same value"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt(secretFixture().Metadata, []byte("same value"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Value.Nonce, second.Value.Nonce) || bytes.Equal(first.Value.Ciphertext, second.Value.Ciphertext) {
		t.Fatal("repeated encryption reused its nonce or ciphertext")
	}
}

func TestSecretEncryptionPersistedV2Envelope(t *testing.T) {
	// Created independently with standard AES-GCM and literal v2 authenticated
	// data. Keep this fixture fixed so an AAD change cannot silently strand records.
	const encoded = `{
		"metadata": {
			"id": "00000000-0000-4000-8000-000000000001",
			"key": {"scope": {"agent_type": "browser"}, "name": "API_TOKEN"},
			"version": 2,
			"created_at": "2026-10-16T11:00:00Z",
			"updated_at": "2026-10-16T12:00:00Z",
			"expires_at": "2026-10-16T13:00:00Z"
		},
		"value": {
			"format": "aes-256-gcm-v2",
			"key_id": "sha256-4884fdaafea47c29fea7159d0daddd9c",
			"nonce": "AAECAwQFBgcICQoL",
			"ciphertext": "8WHobm+UVm3+678hV8zHozqpYJ69mVJp3aji4wolEwTQ5bzaIYw1mQ8WGw=="
		}
	}`
	var record secrets.Secret
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		t.Fatal(err)
	}
	plaintext, err := newSecretCipher(t).Decrypt(record)
	if err != nil || string(plaintext) != "persisted v2 secret fixture" {
		t.Fatalf("persisted v2 envelope could not be decrypted: %v", err)
	}
}

func TestSecretEncryptionAuthenticatesMetadata(t *testing.T) {
	cipher := newSecretCipher(t)
	original, err := cipher.Encrypt(secretFixture().Metadata, []byte("test secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*secrets.Metadata)
	}{
		{"identity", func(m *secrets.Metadata) { m.ID = deliveryID }},
		{"agent type", func(m *secrets.Metadata) { m.Key.Scope.AgentType = "other-agent" }},
		{"name", func(m *secrets.Metadata) { m.Key.Name = "OTHER_TOKEN" }},
		{"version", func(m *secrets.Metadata) { m.Version++ }},
		{"creation", func(m *secrets.Metadata) { m.CreatedAt = m.CreatedAt.Add(-time.Second) }},
		{"update", func(m *secrets.Metadata) { m.UpdatedAt = m.UpdatedAt.Add(time.Second) }},
		{"expiration", func(m *secrets.Metadata) { m.ExpiresAt = m.ExpiresAt.Add(time.Hour) }},
		{"remove expiration", func(m *secrets.Metadata) { m.ExpiresAt = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := original.Clone()
			test.change(&changed.Metadata)
			if err := changed.Metadata.Validate(); err != nil {
				t.Fatalf("tampering fixture must remain structurally valid: %v", err)
			}
			plaintext, err := cipher.Decrypt(changed)
			if plaintext != nil || !errors.Is(err, crypto.ErrDecrypt) {
				t.Fatalf("metadata substitution returned plaintext=%v, error=%v", plaintext != nil, err)
			}
		})
	}
}

func TestSecretEncryptionRejectsCorruptEnvelopes(t *testing.T) {
	cipher := newSecretCipher(t)
	original, err := cipher.Encrypt(secretFixture().Metadata, []byte("test secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*secrets.EncryptedValue)
	}{
		{"format", func(v *secrets.EncryptedValue) { v.Format = "unsupported-v3" }},
		{"previous format", func(v *secrets.EncryptedValue) { v.Format = "aes-256-gcm-v1" }},
		{"key identifier", func(v *secrets.EncryptedValue) { v.KeyID = "unknown-key" }},
		{"missing nonce", func(v *secrets.EncryptedValue) { v.Nonce = nil }},
		{"short nonce", func(v *secrets.EncryptedValue) { v.Nonce = v.Nonce[:11] }},
		{"long nonce", func(v *secrets.EncryptedValue) { v.Nonce = append(v.Nonce, 0) }},
		{"changed nonce", func(v *secrets.EncryptedValue) { v.Nonce[0] ^= 1 }},
		{"missing ciphertext", func(v *secrets.EncryptedValue) { v.Ciphertext = nil }},
		{"truncated tag", func(v *secrets.EncryptedValue) { v.Ciphertext = v.Ciphertext[:15] }},
		{"changed ciphertext", func(v *secrets.EncryptedValue) { v.Ciphertext[0] ^= 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := original.Clone()
			test.change(&changed.Value)
			plaintext, err := cipher.Decrypt(changed)
			if plaintext != nil || !errors.Is(err, crypto.ErrDecrypt) {
				t.Fatalf("corrupt envelope returned plaintext=%v, error=%v", plaintext != nil, err)
			}
		})
	}
	other, err := crypto.NewSecretCipher(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := other.Decrypt(original)
	if plaintext != nil || !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("different key returned plaintext=%v, error=%v", plaintext != nil, err)
	}
	otherEnvelope, err := other.Encrypt(original.Metadata, nil)
	if err != nil {
		t.Fatal(err)
	}
	original.Value.KeyID = otherEnvelope.Value.KeyID
	plaintext, err = other.Decrypt(original)
	if plaintext != nil || !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("forged key identifier bypassed authentication: plaintext=%v, error=%v", plaintext != nil, err)
	}
}

func TestSecretEncryptionCanonicalTimeAndNoFreshnessPolicy(t *testing.T) {
	cipher := newSecretCipher(t)
	metadata := secretFixture().Metadata
	metadata.CreatedAt = time.Date(1500, 1, 1, 0, 0, 0, 123456789, time.UTC)
	metadata.UpdatedAt = metadata.CreatedAt.Add(time.Hour)
	metadata.ExpiresAt = metadata.UpdatedAt.Add(time.Hour)
	secret, err := cipher.Encrypt(metadata, []byte("historical secret"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	var restored secrets.Secret
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	secret = restored
	zone := time.FixedZone("equivalent instant", 3*60*60)
	secret.Metadata.CreatedAt = secret.Metadata.CreatedAt.In(zone)
	secret.Metadata.UpdatedAt = secret.Metadata.UpdatedAt.In(zone)
	secret.Metadata.ExpiresAt = secret.Metadata.ExpiresAt.In(zone)
	plaintext, err := cipher.Decrypt(secret)
	if err != nil || string(plaintext) != "historical secret" {
		t.Fatalf("timezone representation or age changed authentication: %v", err)
	}
}

func TestUnconfiguredSecretCipherFailsClosed(t *testing.T) {
	for _, cipher := range []*crypto.SecretCipher{nil, new(crypto.SecretCipher)} {
		if _, err := cipher.Encrypt(secretFixture().Metadata, []byte("test secret")); err == nil {
			t.Fatal("unconfigured cipher encrypted a value")
		}
		if plaintext, err := cipher.Decrypt(secretFixture()); err == nil || plaintext != nil {
			t.Fatal("unconfigured cipher decrypted a value")
		}
	}
	cipher := newSecretCipher(t)
	invalid := secretFixture().Metadata
	invalid.Version = 0
	if _, err := cipher.Encrypt(invalid, []byte("test secret")); err == nil {
		t.Fatal("invalid metadata was encrypted")
	}
}

func TestSecretCipherConcurrentUse(t *testing.T) {
	cipher := newSecretCipher(t)
	type result struct {
		nonce []byte
		err   error
	}
	const workers = 16
	results := make(chan result, workers)
	for worker := range workers {
		go func() {
			plaintext := []byte{byte(worker), 0, 255}
			secret, err := cipher.Encrypt(secretFixture().Metadata, plaintext)
			if err != nil {
				results <- result{err: err}
				return
			}
			decrypted, err := cipher.Decrypt(secret)
			if err == nil && !bytes.Equal(decrypted, plaintext) {
				err = errors.New("concurrent encryption changed plaintext")
			}
			results <- result{nonce: secret.Value.Nonce, err: err}
		}()
	}
	seen := make(map[string]bool, workers)
	for range workers {
		got := <-results
		if got.err != nil {
			t.Error(got.err)
			continue
		}
		if seen[string(got.nonce)] {
			t.Error("concurrent encryption reused a nonce")
		}
		seen[string(got.nonce)] = true
	}
}

func TestSecretCipherFormattingRedactsKey(t *testing.T) {
	keyBytes := []byte("sensitive-test-encryption-key-12")
	encoded := hex.EncodeToString(keyBytes)
	cipher, err := crypto.NewSecretCipher(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{cipher, *cipher} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if fmt.Sprintf(format, value) != "secret cipher (key redacted)" {
				t.Fatalf("format %q exposed cipher internals", format)
			}
		}
		serialized, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var textLog, jsonLog bytes.Buffer
		slog.New(slog.NewTextHandler(&textLog, nil)).Info("configured", "cipher", value)
		slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("configured", "cipher", value)
		for _, output := range []string{string(serialized), textLog.String(), jsonLog.String()} {
			if strings.Contains(output, encoded) || strings.Contains(output, string(keyBytes)) {
				t.Fatal("formatting disclosed encryption key")
			}
		}
		if string(serialized) != "{}" {
			t.Fatal("cipher JSON exposed internal configuration")
		}
	}
}
