package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	mongosecrets "github.com/defos-ai/commander/providers/mongo/secrets"
	mongostore "github.com/defos-ai/commander/providers/mongo/store"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type secretMetadata struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Version   uint64     `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Status    string     `json:"status"`
}

func TestSecretsWithMongo(t *testing.T) {
	uri := os.Getenv("COMMANDER_TEST_MONGO_URL")
	if uri == "" {
		t.Skip("set COMMANDER_TEST_MONGO_URL to test MongoDB-backed API secrets")
	}
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	server := hostfixtures.Start(t, append(fixture.LocalArgs(), "--database-url="+uri))
	name := "HOST_TEST_" + id.New()
	path := hostfixtures.SecretsPath + "/" + name
	created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, fmt.Sprintf(`{"name":%q,"value":%q}`, name, hostfixtures.SecretTestValue)), http.StatusCreated)
	defer func() {
		// Read the current version so failure midway through replacement still
		// cleans up only this test's record; shared catalogs are never dropped.
		current := secretRequest(t, server, http.MethodGet, path, "")
		if current.Status == http.StatusNotFound {
			return
		}
		metadata := decodeSecretResponse[secretMetadata](t, current, http.StatusOK)
		if metadata.ID != created.ID {
			t.Error("refusing to delete a different secret identity")
			return
		}
		deleted := secretRequest(t, server, http.MethodDelete, fmt.Sprintf("%s?id=%s&version=%d", path, metadata.ID, metadata.Version), "")
		if deleted.Status != http.StatusNoContent {
			t.Error("could not delete the MongoDB test secret")
		}
	}()
	replaced := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, path, fmt.Sprintf(`{"id":%q,"version":1,"value":%q}`, created.ID, hostfixtures.SecretTestReplacement)), http.StatusOK)
	if replaced.Version != 2 || replaced.ID != created.ID {
		t.Fatal("MongoDB-backed API did not replace the saved identity")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	database, err := mongostore.Open(ctx, uri)
	if err != nil {
		t.Fatal("could not reopen test MongoDB")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := database.Close(cleanup); err != nil {
			t.Error("could not close test MongoDB client")
		}
	}()
	store, err := mongosecrets.NewSecretMongoStore(ctx, database)
	if err != nil {
		t.Fatal("could not reopen MongoDB secret store")
	}
	stored, err := store.Get(ctx, secretmodel.Key{Name: name})
	if err != nil {
		t.Fatal("API did not persist secret in MongoDB")
	}
	cipher, err := crypto.NewSecretCipher(hostfixtures.SecretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(stored)
	if err != nil || string(plaintext) != hostfixtures.SecretTestReplacement {
		t.Fatal("persisted MongoDB secret did not authenticate or decrypt")
	}
	requests, err := mongosecrets.NewRequestMongoStore(ctx, database)
	if err != nil {
		t.Fatal("could not reopen MongoDB request store")
	}
	agentID, err := id.AgentID("builder")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	request := secretmodel.Request{
		ID: id.New(), Key: stored.Metadata.Key, AgentID: agentID, AgentType: "builder-agent",
		Status: secretmodel.Pending, RequestedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	if err := requests.Create(ctx, request); err != nil {
		t.Fatal("could not save MongoDB secret request")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := requests.Cancel(cleanup, request.ID, time.Now()); err != nil {
			t.Error("could not cancel this test's MongoDB request")
		}
	}()
	pending := decodeResponse[struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
	}](t, secretRequest(t, server, http.MethodGet, "/api/v1/secret-requests?agent_id="+agentID, ""))
	if len(pending.Items) != 1 || pending.Items[0].ID != request.ID || pending.Items[0].Status != "awaiting_delivery" {
		t.Fatal("API did not read pending requests and metadata from MongoDB")
	}
	if _, err := os.Stat(filepath.Join(fixture.Root, "database", "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("MongoDB configuration also wrote an FS secret store")
	}
	if _, err := os.Stat(filepath.Join(fixture.Root, "database", "secret-requests.json")); !os.IsNotExist(err) {
		t.Fatal("MongoDB configuration also wrote an FS request store")
	}
}

func secretRequest(t *testing.T, server *hostfixtures.API, method, path, body string) hostfixtures.Response {
	t.Helper()
	result := server.Request(t, method, path, hostfixtures.Authenticate, hostfixtures.SecretBody(body, "application/json"))
	assertNoSecretResponse(t, result)
	return result
}

func assertNoSecretResponse(t *testing.T, result hostfixtures.Response) {
	t.Helper()
	for _, private := range []string{hostfixtures.SecretTestKey, hostfixtures.SecretTestValue, hostfixtures.SecretTestReplacement, `"value"`, `"ciphertext"`, `"nonce"`, `"key_id"`, `"format"`, "private-value", "private-query-value", "private-invalid-id"} {
		if strings.Contains(result.Body, private) {
			t.Fatal("API response exposed a secret, encryption envelope or rejected input")
		}
	}
}

func decodeSecretResponse[T any](t *testing.T, result hostfixtures.Response, status int) T {
	t.Helper()
	assertNoSecretResponse(t, result)
	if result.Status != status {
		t.Fatalf("secret API returned %d, want %d", result.Status, status)
	}
	var value T
	decoder := json.NewDecoder(strings.NewReader(result.Body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal("secret response was not the expected metadata-only JSON shape")
	}
	return value
}
