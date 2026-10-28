package secrets_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/domain/secrets"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/secrets"
)

func TestScopeAndInvalidIdentities(t *testing.T) {
	scope, err := secrets.NewScope("browser")
	if err != nil || scope.AgentType != "browser" {
		t.Fatalf("scope = %+v, error = %v", scope, err)
	}
	for _, agentType := range []string{"", "../browser", "browser/other", "Browser", "browser--agent"} {
		if _, err := secrets.NewScope(agentType); err == nil {
			t.Errorf("invalid agent type accepted: %q", agentType)
		}
	}

	key := fixtures.Secret().Metadata.Key
	key.Name = "../API_TOKEN"
	if err := key.Validate(); err == nil {
		t.Error("path-like secret name accepted")
	}
	metadata := fixtures.Secret().Metadata
	metadata.ID = "secret-1"
	if err := metadata.Validate(); err == nil {
		t.Error("non-UUID secret identity accepted")
	}
	request := fixtures.Request()
	request.AgentID = "builder"
	if err := request.Validate(); err == nil {
		t.Error("agent type accepted as recipient instance ID")
	}
}

func TestAvailabilityBoundaries(t *testing.T) {
	metadata := fixtures.Secret().Metadata
	if err := metadata.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := secrets.AvailabilityAt(nil, fixtures.RequestedAt()); got != secrets.Missing {
		t.Fatalf("absent secret availability = %q", got)
	}
	for _, test := range []struct {
		name string
		now  time.Time
		want secrets.Availability
	}{
		{"before expiry", metadata.ExpiresAt.Add(-time.Nanosecond), secrets.Available},
		{"at expiry", metadata.ExpiresAt, secrets.Expired},
		{"after expiry", metadata.ExpiresAt.Add(time.Nanosecond), secrets.Expired},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := secrets.AvailabilityAt(&metadata, test.now); got != test.want {
				t.Fatalf("availability = %q, want %q", got, test.want)
			}
		})
	}
	metadata.ExpiresAt = time.Time{}
	if err := metadata.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := secrets.AvailabilityAt(&metadata, fixtures.RequestedAt().AddDate(10, 0, 0)); got != secrets.Available {
		t.Fatalf("secret without expiry availability = %q", got)
	}
}

func TestEncryptedSecretValidationAndClone(t *testing.T) {
	original := fixtures.Secret()
	if err := original.Validate(); err != nil {
		t.Fatal(err)
	}
	clone := original.Clone()
	clone.Value.Nonce[0] = 'X'
	clone.Value.Ciphertext[0] = 'X'
	if string(original.Value.Nonce) != "nonce-fixture" || string(original.Value.Ciphertext) != "private-payload-fixture" {
		t.Fatal("changing cloned encrypted bytes changed the original")
	}
	for _, test := range []struct {
		name   string
		change func(*secrets.Secret)
	}{
		{"missing ciphertext", func(s *secrets.Secret) { s.Value.Ciphertext = nil }},
		{"missing nonce", func(s *secrets.Secret) { s.Value.Nonce = nil }},
		{"zero business version", func(s *secrets.Secret) { s.Metadata.Version = 0 }},
		{"expiry before update", func(s *secrets.Secret) { s.Metadata.ExpiresAt = s.Metadata.UpdatedAt }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := fixtures.Secret()
			test.change(&invalid)
			if err := invalid.Validate(); err == nil {
				t.Fatal("invalid encrypted secret accepted")
			}
		})
	}
}

func TestPublicJSONExcludesSecretValue(t *testing.T) {
	secret := fixtures.Secret()
	request, err := fixtures.Request().Fulfill(secret.Metadata, fixtures.DeliveryID, fixtures.RequestedAt().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []any{secret.Metadata, request} {
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			`"value":`, `"plaintext":`, `"ciphertext":`, `"nonce":`, `"key_id":`, `"format":`,
			string(secret.Value.Ciphertext), base64.StdEncoding.EncodeToString(secret.Value.Ciphertext),
			string(secret.Value.Nonce), base64.StdEncoding.EncodeToString(secret.Value.Nonce),
		} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%T JSON disclosed a private value or encryption field", snapshot)
			}
		}
	}
}
