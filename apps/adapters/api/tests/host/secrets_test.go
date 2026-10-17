package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/cli"
	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
	mongosecrets "github.com/defos-ai/commander/providers/mongo/secrets"
	mongostore "github.com/defos-ai/commander/providers/mongo/store"
)

const (
	secretTestKey         = "014ac583269fe047014ac583269fe047014ac583269fe047014ac583269fe047"
	secretTestValue       = "host-test-private-token-original"
	secretTestReplacement = "host-test-private-token-replaced"
	secretsPath           = "/api/v1/secrets"
	secretPath            = secretsPath + "/builder-agent/GITHUB_TOKEN"
)

type secretMetadata struct {
	ID        string     `json:"id"`
	AgentType string     `json:"agent_type"`
	Name      string     `json:"name"`
	Version   uint64     `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Status    string     `json:"status"`
}

func TestSecretAPILifecyclePersistsOnlyEncryptedValues(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	server := startAPI(t, fixture.localArgs())
	createBody := `{"agent_type":"builder-agent","name":"GITHUB_TOKEN","value":"` + secretTestValue + `"}`
	createdResponse := secretRequest(t, server, http.MethodPost, secretsPath, createBody)
	created := decodeSecretResponse[secretMetadata](t, createdResponse, http.StatusCreated)
	if createdResponse.header.Get("Location") != secretPath || created.AgentType != "builder-agent" || created.Name != "GITHUB_TOKEN" || created.Version != 1 || created.Status != "available" || created.ExpiresAt != nil || created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatal("create did not return the new scoped metadata and its resource location")
	}
	assertSecretError(t, secretRequest(t, server, http.MethodPost, secretsPath, createBody), http.StatusConflict, "secret_conflict")
	fetched := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodGet, secretPath, ""), http.StatusOK)
	if fetched.ID != created.ID || fetched.Version != 1 {
		t.Fatal("metadata lookup did not return the created identity")
	}
	assertSecretOnDisk(t, fixture.root, created, secretTestValue)

	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	replaceBody := fmt.Sprintf(`{"id":%q,"version":1,"value":%q,"expires_at":%q}`, created.ID, secretTestReplacement, expiry.Format(time.RFC3339Nano))
	replaced := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, secretPath, replaceBody), http.StatusOK)
	if replaced.ID != created.ID || replaced.Version != 2 || !replaced.CreatedAt.Equal(created.CreatedAt) || replaced.UpdatedAt.Before(created.UpdatedAt) || replaced.ExpiresAt == nil || !replaced.ExpiresAt.Equal(expiry) {
		t.Fatal("replacement did not preserve identity, increment version and apply expiry")
	}
	assertSecretOnDisk(t, fixture.root, replaced, secretTestReplacement)
	assertSecretError(t, secretRequest(t, server, http.MethodPut, secretPath, replaceBody), http.StatusConflict, "secret_conflict")
	oldDelete := secretPath + "?id=" + created.ID + "&version=1"
	assertSecretError(t, secretRequest(t, server, http.MethodDelete, oldDelete, ""), http.StatusConflict, "secret_conflict")
	deletePath := secretPath + "?id=" + created.ID + "&version=2"
	for range 2 {
		result := secretRequest(t, server, http.MethodDelete, deletePath, "")
		if result.status != http.StatusNoContent || result.body != "" {
			t.Fatal("deletion and its exact retry must succeed without a response body")
		}
	}
	assertSecretError(t, secretRequest(t, server, http.MethodGet, secretPath, ""), http.StatusNotFound, "secret_not_found")
	recreated := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath, createBody), http.StatusCreated)
	if recreated.ID == created.ID || recreated.Version != 1 {
		t.Fatal("recreating a deleted secret reused its retired identity")
	}
	assertSecretError(t, secretRequest(t, server, http.MethodDelete, oldDelete, ""), http.StatusConflict, "secret_conflict")
	assertSecretError(t, secretRequest(t, server, http.MethodPut, secretPath, replaceBody), http.StatusConflict, "secret_conflict")
	server.stop(t)
	server = startAPI(t, fixture.localArgs())
	reopened := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodGet, secretPath, ""), http.StatusOK)
	if reopened.ID != recreated.ID || reopened.Version != 1 {
		t.Fatal("restarted API lost the recreated secret metadata")
	}
	assertSecretOnDisk(t, fixture.root, reopened, secretTestValue)
}

func TestSecretInventoryScopesAndProjectsExpiry(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	store, err := fssecrets.NewSecretFsStore(filepath.Join(fixture.root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewSecretCipher(secretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2000, 1, 1, 0, 0, 0, 123456789, time.UTC)
	expired, err := cipher.Encrypt(secretmodel.Metadata{
		ID:      "00000000-0000-4000-8000-000000000001",
		Key:     secretmodel.Key{Scope: secretmodel.Scope{AgentType: "builder-agent"}, Name: "OLD_TOKEN"},
		Version: 1, CreatedAt: created, UpdatedAt: created, ExpiresAt: created.Add(time.Hour),
	}, []byte(secretTestValue))
	if err != nil {
		t.Fatal("could not encrypt expiry fixture")
	}
	if err := store.Create(t.Context(), expired); err != nil {
		t.Fatal(err)
	}
	server := startAPI(t, fixture.localArgs())
	for _, body := range []string{
		`{"agent_type":"builder-agent","name":"A_TOKEN","value":""}`,
		`{"agent_type":"web-agent","name":"A_TOKEN","value":""}`,
	} {
		decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath, body), http.StatusCreated)
	}
	list := decodeSecretResponse[struct {
		Items []secretMetadata `json:"items"`
	}](t, secretRequest(t, server, http.MethodGet, secretsPath+"?agent_type=builder-agent", ""), http.StatusOK)
	if len(list.Items) != 2 || list.Items[0].Name != "A_TOKEN" || list.Items[0].Status != "available" || list.Items[1].Name != "OLD_TOKEN" || list.Items[1].Status != "expired" {
		t.Fatal("inventory did not sort and restrict metadata to the agent type including expiry")
	}
	old := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodGet, secretsPath+"/builder-agent/OLD_TOKEN", ""), http.StatusOK)
	if old.Status != "expired" || !old.CreatedAt.Equal(created) {
		t.Fatal("expired secret metadata was changed or timestamp precision was lost")
	}
	other := decodeSecretResponse[struct {
		Items []secretMetadata `json:"items"`
	}](t, secretRequest(t, server, http.MethodGet, secretsPath+"?agent_type=web-agent", ""), http.StatusOK)
	if len(other.Items) != 1 || other.Items[0].Name != "A_TOKEN" || other.Items[0].ID == list.Items[0].ID {
		t.Fatal("agent-type filtering returned another scope")
	}
	empty := decodeSecretResponse[struct {
		Items []secretMetadata `json:"items"`
	}](t, secretRequest(t, server, http.MethodGet, secretsPath+"?agent_type=unknown-agent", ""), http.StatusOK)
	if empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal("missing scope must return an empty array")
	}
	if _, err := store.Get(t.Context(), expired.Metadata.Key); err != nil {
		t.Fatal("inventory deleted the expired record")
	}
	renewed := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, secretsPath+"/builder-agent/OLD_TOKEN", fmt.Sprintf(`{"id":%q,"version":1,"value":""}`, old.ID)), http.StatusOK)
	if renewed.ExpiresAt != nil || renewed.Status != "available" || renewed.Version != 2 {
		t.Fatal("replacement with omitted expiry did not clear the previous expiration")
	}
}

func TestSecretRoutesRequireAuthenticationAndExplicitEncryptionKey(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, fixture.localArgs())
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, secretsPath + "?agent_type=builder-agent"},
		{http.MethodPost, secretsPath}, {http.MethodGet, secretPath},
		{http.MethodPut, secretPath}, {http.MethodDelete, secretPath},
	} {
		assertSecretError(t, server.request(t, route.method, route.path), http.StatusUnauthorized, "authentication_required")
		assertSecretError(t, server.request(t, route.method, route.path, authenticate), http.StatusServiceUnavailable, "secrets_unavailable")
	}
	assertHealthy(t, server.request(t, http.MethodGet, "/readyz"))
	if server.request(t, http.MethodGet, "/api/v1/agents", authenticate).status != http.StatusOK {
		t.Fatal("missing optional key disabled existing API routes")
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "database", "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("disabled secrets opened or created a secret database")
	}
}

func TestSecretRequestsRejectMalformedAndAmbiguousInput(t *testing.T) {
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	server := startAPI(t, fixture.localArgs())
	for _, tc := range []struct{ name, method, path, body string }{
		{"missing value", "POST", secretsPath, `{"agent_type":"builder-agent","name":"TOKEN"}`},
		{"null value", "POST", secretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":null}`},
		{"nonstring value", "POST", secretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":1}`},
		{"unknown field", "POST", secretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":"","ciphertext":"private-value"}`},
		{"obsolete profile", "POST", secretsPath, `{"agent_type":"builder-agent","profile":"default","name":"TOKEN","value":""}`},
		{"multiple bodies", "POST", secretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":""}{}`},
		{"empty body", "POST", secretsPath, ""},
		{"null body", "POST", secretsPath, "null"},
		{"invalid scope", "POST", secretsPath, `{"agent_type":"../builder","name":"TOKEN","value":""}`},
		{"invalid name", "POST", secretsPath, `{"agent_type":"builder-agent","name":"../TOKEN","value":""}`},
		{"past expiry", "POST", secretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":"","expires_at":"2000-01-01T00:00:00Z"}`},
		{"zero expiry", "POST", secretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":"","expires_at":"0001-01-01T00:00:00Z"}`},
		{"unknown list query", "GET", secretsPath + "?agent_type=builder-agent&value=private-query-value", ""},
		{"missing scope", "GET", secretsPath, ""},
		{"duplicated scope", "GET", secretsPath + "?agent_type=builder-agent&agent_type=web-agent", ""},
		{"obsolete profile query", "GET", secretsPath + "?agent_type=builder-agent&profile=default", ""},
		{"delete missing precondition", "DELETE", secretPath, ""},
		{"delete malformed id", "DELETE", secretPath + "?id=private-invalid-id&version=1", ""},
		{"delete zero version", "DELETE", secretPath + "?id=00000000-0000-4000-8000-000000000001&version=0", ""},
		{"delete duplicate version", "DELETE", secretPath + "?id=00000000-0000-4000-8000-000000000001&version=1&version=2", ""},
		{"replace missing identity", "PUT", secretPath, `{"version":1,"value":""}`},
		{"replace missing value", "PUT", secretPath, `{"id":"00000000-0000-4000-8000-000000000001","version":1}`},
		{"replace obsolete profile", "PUT", secretPath, `{"id":"00000000-0000-4000-8000-000000000001","version":1,"value":"","profile":"default"}`},
		{"replace zero expiry", "PUT", secretPath, `{"id":"00000000-0000-4000-8000-000000000001","version":1,"value":"","expires_at":"0001-01-01T00:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertSecretError(t, secretRequest(t, server, tc.method, tc.path, tc.body), http.StatusBadRequest, "invalid_request")
		})
	}
	for _, contentType := range []string{"", "text/plain"} {
		result := server.request(t, http.MethodPost, secretsPath, authenticate, secretBody(`{"agent_type":"builder-agent","name":"TOKEN","value":""}`, contentType))
		assertSecretError(t, result, http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	large := `{"agent_type":"builder-agent","name":"TOKEN","value":"` + strings.Repeat("x", 1<<20) + `"}`
	assertSecretError(t, secretRequest(t, server, http.MethodPost, secretsPath, large), http.StatusRequestEntityTooLarge, "request_too_large")
	list := decodeSecretResponse[struct {
		Items []secretMetadata `json:"items"`
	}](t, secretRequest(t, server, http.MethodGet, secretsPath+"?agent_type=builder-agent", ""), http.StatusOK)
	if len(list.Items) != 0 {
		t.Fatal("rejected request mutated the secret inventory")
	}
}

func TestSecretEncryptionKeyFailsBeforeStartupAndNeverAppearsInHelp(t *testing.T) {
	fixture := newFixture(t)
	const invalid = "private-invalid-encryption-key"
	t.Setenv("SECRET_ENCRYPTION_KEY", invalid)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var diagnostic bytes.Buffer
	ready := false
	err := cli.RunWithOptions(ctx, fixture.localArgs(), &diagnostic, cli.Options{Ready: func(net.Addr) { ready = true }})
	if err == nil || ctx.Err() != nil || ready {
		t.Fatal("invalid encryption key did not fail before serving requests")
	}
	if !strings.Contains(err.Error(), "SECRET_ENCRYPTION_KEY") || strings.Contains(err.Error()+diagnostic.String(), invalid) {
		t.Fatal("invalid key diagnostic must name its environment variable without its value")
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "database")); !os.IsNotExist(err) {
		t.Fatal("invalid encryption key opened the database")
	}
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	diagnostic.Reset()
	if code := cli.Main([]string{"--help"}, &diagnostic, &diagnostic); code != 0 {
		t.Fatal("help failed with configured secret encryption")
	}
	if strings.Contains(diagnostic.String(), secretTestKey) {
		t.Fatal("help exposed the encryption key")
	}
}

// Provider failures are exercised at the HTTP boundary without corrupting a
// real database or making uncertain outcomes depend on filesystem timing.
func TestSecretFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	cipher, err := crypto.NewSecretCipher(secretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code   string
		status int
		err    error
	}{
		{"secret_write_uncertain", 503, errors.Join(secretmodel.ErrWriteUncertain, context.DeadlineExceeded)},
		{"request_timeout", 504, context.DeadlineExceeded},
		{"request_canceled", 503, context.Canceled},
		{"internal_error", 500, errors.New(secretTestValue + " private backend failure")},
	} {
		t.Run(tc.code, func(t *testing.T) {
			store := &failingSecretCreate{err: tc.err}
			service, err := secretservice.NewService(store, cipher)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			endpoints.RegisterSecrets(mux, service)
			request := httptest.NewRequest(http.MethodPost, secretsPath, strings.NewReader(`{"agent_type":"builder-agent","name":"TOKEN","value":"`+secretTestValue+`"}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, request)
			assertSecretError(t, response{status: recorder.Code, header: recorder.Header(), body: recorder.Body.String()}, tc.status, tc.code)
			if store.attempts != 1 || store.remaining <= 0 || store.remaining > 10*time.Second {
				t.Fatal("secret mutation was retried or lost its bounded request context")
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("failure response may be cached")
			}
		})
	}
}

type failingSecretCreate struct {
	secretmodel.SecretStore
	err       error
	attempts  int
	remaining time.Duration
}

func (store *failingSecretCreate) Create(ctx context.Context, _ secretmodel.Secret) error {
	store.attempts++
	deadline, _ := ctx.Deadline()
	store.remaining = time.Until(deadline)
	return store.err
}

func TestSecretOpenAPIDescribesWriteOnlyValuesAndMetadata(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, append(fixture.localArgs(), "--swagger=true"))
	result := server.request(t, http.MethodGet, "/openapi.json", authenticate)
	var document struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if result.status != http.StatusOK || json.Unmarshal([]byte(result.body), &document) != nil {
		t.Fatal("could not read API documentation")
	}
	if strings.Contains(result.body, `"profile"`) || strings.Contains(result.body, `"secret_profile"`) || strings.Contains(result.body, "{profile}") {
		t.Fatal("API documentation still exposes secret profiles")
	}
	for path, methods := range map[string][]string{
		secretsPath:                          {"get", "post"},
		secretsPath + "/{agent_type}/{name}": {"get", "put", "delete"},
	} {
		for _, method := range methods {
			if len(document.Paths[path][method]) == 0 {
				t.Errorf("missing documented %s %s", method, path)
			}
		}
	}
	properties := func(name string) map[string]struct {
		WriteOnly bool `json:"writeOnly"`
	} {
		var schema struct {
			Properties map[string]struct {
				WriteOnly bool `json:"writeOnly"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(document.Components.Schemas[name], &schema); err != nil {
			t.Fatalf("could not read %s schema", name)
		}
		return schema.Properties
	}
	for _, name := range []string{"CreateSecretRequest", "ReplaceSecretRequest"} {
		if !properties(name)["value"].WriteOnly {
			t.Errorf("%s value is not write-only", name)
		}
	}
	metadata := properties("Secret")
	if len(metadata) == 0 {
		t.Fatal("secret response schema is missing")
	}
	for _, name := range []string{"value", "ciphertext", "nonce", "key_id", "format"} {
		if _, present := metadata[name]; present {
			t.Errorf("secret response schema contains sensitive field %s", name)
		}
	}
	readiness := properties("AgentReadiness")
	for _, name := range []string{"agent_type", "missing_requirements"} {
		if _, present := readiness[name]; !present {
			t.Errorf("operation readiness schema is missing %s", name)
		}
	}
	if _, present := readiness["missing_secrets"]; present {
		t.Error("operation readiness schema duplicates managed secret requirements")
	}
	requirement := properties("ActionRequirement")
	if _, present := requirement["sensitive"]; !present {
		t.Error("requirement schema is missing the sensitive flag")
	}
	if _, present := requirement["secret"]; present {
		t.Error("requirement schema still exposes the old secret flag")
	}
}

func TestSecretsWithMongo(t *testing.T) {
	uri := os.Getenv("COMMANDER_TEST_MONGO_URL")
	if uri == "" {
		t.Skip("set COMMANDER_TEST_MONGO_URL to test MongoDB-backed API secrets")
	}
	fixture := newFixture(t)
	t.Setenv("SECRET_ENCRYPTION_KEY", secretTestKey)
	server := startAPI(t, append(fixture.localArgs(), "--database-url="+uri))
	name := "HOST_TEST_" + id.New()
	path := secretsPath + "/builder-agent/" + name
	created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, secretsPath, fmt.Sprintf(`{"agent_type":"builder-agent","name":%q,"value":%q}`, name, secretTestValue)), http.StatusCreated)
	defer func() {
		// Read the current version so failure midway through replacement still
		// cleans up only this test's record; shared catalogs are never dropped.
		current := secretRequest(t, server, http.MethodGet, path, "")
		if current.status == http.StatusNotFound {
			return
		}
		metadata := decodeSecretResponse[secretMetadata](t, current, http.StatusOK)
		if metadata.ID != created.ID {
			t.Error("refusing to delete a different secret identity")
			return
		}
		deleted := secretRequest(t, server, http.MethodDelete, fmt.Sprintf("%s?id=%s&version=%d", path, metadata.ID, metadata.Version), "")
		if deleted.status != http.StatusNoContent {
			t.Error("could not delete the MongoDB test secret")
		}
	}()
	replaced := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, path, fmt.Sprintf(`{"id":%q,"version":1,"value":%q}`, created.ID, secretTestReplacement)), http.StatusOK)
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
	stored, err := store.Get(ctx, secretmodel.Key{Scope: secretmodel.Scope{AgentType: "builder-agent"}, Name: name})
	if err != nil {
		t.Fatal("API did not persist secret in MongoDB")
	}
	cipher, err := crypto.NewSecretCipher(secretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(stored)
	if err != nil || string(plaintext) != secretTestReplacement {
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
		ID: id.New(), Key: stored.Metadata.Key, AgentID: agentID,
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
	}](t, secretRequest(t, server, http.MethodGet, "/api/v1/secrets/requests?agent_id="+agentID, ""))
	if len(pending.Items) != 1 || pending.Items[0].ID != request.ID || pending.Items[0].Status != "awaiting_delivery" {
		t.Fatal("API did not read pending requests and metadata from MongoDB")
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "database", "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("MongoDB configuration also wrote an FS secret store")
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "database", "secret-requests.json")); !os.IsNotExist(err) {
		t.Fatal("MongoDB configuration also wrote an FS request store")
	}
}

func secretBody(body, contentType string) func(*http.Request) {
	return func(request *http.Request) {
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = int64(len(body))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
	}
}

func secretRequest(t *testing.T, server *runningAPI, method, path, body string) response {
	t.Helper()
	result := server.request(t, method, path, authenticate, secretBody(body, "application/json"))
	assertNoSecretResponse(t, result)
	return result
}

func assertNoSecretResponse(t *testing.T, result response) {
	t.Helper()
	for _, private := range []string{secretTestKey, secretTestValue, secretTestReplacement, `"value"`, `"ciphertext"`, `"nonce"`, `"key_id"`, `"format"`, "private-value", "private-query-value", "private-invalid-id"} {
		if strings.Contains(result.body, private) {
			t.Fatal("API response exposed a secret, encryption envelope or rejected input")
		}
	}
}

func decodeSecretResponse[T any](t *testing.T, result response, status int) T {
	t.Helper()
	assertNoSecretResponse(t, result)
	if result.status != status {
		t.Fatalf("secret API returned %d, want %d", result.status, status)
	}
	var value T
	decoder := json.NewDecoder(strings.NewReader(result.body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal("secret response was not the expected metadata-only JSON shape")
	}
	return value
}

func assertSecretError(t *testing.T, result response, status int, code string) {
	t.Helper()
	assertNoSecretResponse(t, result)
	var payload map[string]string
	if result.status != status || json.Unmarshal([]byte(result.body), &payload) != nil || len(payload) != 1 || payload["error"] != code {
		t.Fatalf("secret API returned %d, want %d with error %q", result.status, status, code)
	}
}

func assertSecretOnDisk(t *testing.T, root string, expected secretMetadata, plaintext string) {
	t.Helper()
	store, err := fssecrets.NewSecretFsStore(filepath.Join(root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(t.Context(), secretmodel.Key{Scope: secretmodel.Scope{AgentType: expected.AgentType}, Name: expected.Name})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewSecretCipher(secretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := cipher.Decrypt(stored)
	if err != nil || string(decrypted) != plaintext || stored.Metadata.ID != expected.ID || stored.Metadata.Version != expected.Version {
		t.Fatal("reopened storage did not authenticate and decrypt the submitted value with its current metadata")
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, private := range []string{secretTestKey, secretTestValue, secretTestReplacement} {
			if bytes.Contains(data, []byte(private)) {
				t.Fatal("API stored plaintext or key material on disk")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
