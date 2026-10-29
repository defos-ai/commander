package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	secretendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/secrets"
	"github.com/defos-ai/commander/apps/adapters/api/tests/fakes"
	hostfixtures "github.com/defos-ai/commander/apps/adapters/api/tests/fixtures/host"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
	mongosecrets "github.com/defos-ai/commander/providers/mongo/secrets"
	mongostore "github.com/defos-ai/commander/providers/mongo/store"
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

func TestSecretAPILifecyclePersistsOnlyEncryptedValues(t *testing.T) {
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	createBody := `{"name":"GITHUB_TOKEN","value":"` + hostfixtures.SecretTestValue + `"}`
	createdResponse := secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, createBody)
	created := decodeSecretResponse[secretMetadata](t, createdResponse, http.StatusCreated)
	if createdResponse.Header.Get("Location") != hostfixtures.SecretPath || created.Name != "GITHUB_TOKEN" || created.Version != 1 || created.Status != "available" || created.ExpiresAt != nil || created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatal("create did not return the new shared metadata and its resource location")
	}
	assertSecretError(t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, createBody), http.StatusConflict, "secret_conflict")
	fetched := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodGet, hostfixtures.SecretPath, ""), http.StatusOK)
	if fetched.ID != created.ID || fetched.Version != 1 {
		t.Fatal("metadata lookup did not return the created identity")
	}
	assertSecretOnDisk(t, fixture.Root, created, hostfixtures.SecretTestValue)

	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	replaceBody := fmt.Sprintf(`{"id":%q,"version":1,"value":%q,"expires_at":%q}`, created.ID, hostfixtures.SecretTestReplacement, expiry.Format(time.RFC3339Nano))
	replaced := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPut, hostfixtures.SecretPath, replaceBody), http.StatusOK)
	if replaced.ID != created.ID || replaced.Version != 2 || !replaced.CreatedAt.Equal(created.CreatedAt) || replaced.UpdatedAt.Before(created.UpdatedAt) || replaced.ExpiresAt == nil || !replaced.ExpiresAt.Equal(expiry) {
		t.Fatal("replacement did not preserve identity, increment version and apply expiry")
	}
	assertSecretOnDisk(t, fixture.Root, replaced, hostfixtures.SecretTestReplacement)
	assertSecretError(t, secretRequest(t, server, http.MethodPut, hostfixtures.SecretPath, replaceBody), http.StatusConflict, "secret_conflict")
	oldDelete := hostfixtures.SecretPath + "?id=" + created.ID + "&version=1"
	assertSecretError(t, secretRequest(t, server, http.MethodDelete, oldDelete, ""), http.StatusConflict, "secret_conflict")
	deletePath := hostfixtures.SecretPath + "?id=" + created.ID + "&version=2"
	for range 2 {
		result := secretRequest(t, server, http.MethodDelete, deletePath, "")
		if result.Status != http.StatusNoContent || result.Body != "" {
			t.Fatal("deletion and its exact retry must succeed without a response body")
		}
	}
	assertSecretError(t, secretRequest(t, server, http.MethodGet, hostfixtures.SecretPath, ""), http.StatusNotFound, "secret_not_found")
	recreated := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, createBody), http.StatusCreated)
	if recreated.ID == created.ID || recreated.Version != 1 {
		t.Fatal("recreating a deleted secret reused its retired identity")
	}
	assertSecretError(t, secretRequest(t, server, http.MethodDelete, oldDelete, ""), http.StatusConflict, "secret_conflict")
	assertSecretError(t, secretRequest(t, server, http.MethodPut, hostfixtures.SecretPath, replaceBody), http.StatusConflict, "secret_conflict")
	server.Stop(t)
	server = hostfixtures.Start(t, fixture.LocalArgs())
	reopened := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodGet, hostfixtures.SecretPath, ""), http.StatusOK)
	if reopened.ID != recreated.ID || reopened.Version != 1 {
		t.Fatal("restarted API lost the recreated secret metadata")
	}
	assertSecretOnDisk(t, fixture.Root, reopened, hostfixtures.SecretTestValue)
}

func TestInjectedSecretCipherOverridesEnvironment(t *testing.T) {
	for _, test := range []struct{ name, environment string }{
		{"missing", ""},
		{"malformed", "invalid-environment-key"},
		{"different", strings.Repeat("a", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := hostfixtures.New(t)
			t.Setenv("SECRETS_ENCRYPTION_KEY", test.environment)
			cipher, err := crypto.NewSecretCipher(hostfixtures.SecretTestKey)
			if err != nil {
				t.Fatal(err)
			}
			server := hostfixtures.Start(t, fixture.LocalArgs(), cli.Options{SecretCipher: cipher})
			body := `{"name":"GITHUB_TOKEN","value":"` + hostfixtures.SecretTestValue + `"}`
			created := decodeSecretResponse[secretMetadata](t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, body), http.StatusCreated)
			assertSecretOnDisk(t, fixture.Root, created, hostfixtures.SecretTestValue)
			server.Stop(t)
			if os.Getenv("SECRETS_ENCRYPTION_KEY") != test.environment {
				t.Fatal("injected cipher changed process environment")
			}
		})
	}
}

func TestUninitializedInjectedSecretCipherFailsBeforeStartup(t *testing.T) {
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var diagnostic bytes.Buffer
	ready := false
	options := cli.Options{SecretCipher: new(crypto.SecretCipher), Ready: func(net.Addr) { ready = true }}
	err := cli.RunWithOptions(ctx, fixture.LocalArgs(), &diagnostic, options)
	if !errors.Is(err, crypto.ErrMissingKey) || ready || ctx.Err() != nil {
		t.Fatal("uninitialized injected cipher did not fail before serving requests")
	}
	for _, name := range []string{"bus", "storage", "database"} {
		if _, err := os.Stat(filepath.Join(fixture.Root, name)); !os.IsNotExist(err) {
			t.Fatal("uninitialized injected cipher opened a provider")
		}
	}
	if strings.Contains(err.Error()+diagnostic.String(), hostfixtures.SecretTestKey) {
		t.Fatal("cipher validation exposed the environment key")
	}
	if err := cli.RunWithOptions(ctx, []string{"--help"}, &diagnostic, options); err != nil {
		t.Fatal("help required an initialized cipher")
	}
}

func TestSecretInventorySharesNamesAndProjectsExpiry(t *testing.T) {
	fixture := hostfixtures.New(t)
	requests, store := hostfixtures.SecretRequestStores(t, fixture)
	cipher, err := crypto.NewSecretCipher(hostfixtures.SecretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2000, 1, 1, 0, 0, 0, 123456789, time.UTC)
	expired, err := cipher.Encrypt(secretmodel.Metadata{
		ID:      "00000000-0000-4000-8000-000000000001",
		Key:     secretmodel.Key{Name: "OLD_TOKEN"},
		Version: 1, CreatedAt: created, UpdatedAt: created, ExpiresAt: created.Add(time.Hour),
	}, []byte(hostfixtures.SecretTestValue))
	if err != nil {
		t.Fatal("could not encrypt expiry fixture")
	}
	if err := store.Create(t.Context(), expired); err != nil {
		t.Fatal(err)
	}
	handler := hostfixtures.SecretEndpoints(hostfixtures.PendingRequestService(t, store), requests)
	send := func(method, path, body string) hostfixtures.Response {
		return hostfixtures.SecretEndpointResponse(t.Context(), handler, method, path, body)
	}
	const create = `{"name":"A_TOKEN","value":""}`
	decodeSecretResponse[secretMetadata](t, send(http.MethodPost, hostfixtures.SecretsPath, create), http.StatusCreated)
	assertSecretError(t, send(http.MethodPost, hostfixtures.SecretsPath, create), http.StatusConflict, "secret_conflict")
	list := decodeSecretResponse[struct {
		Items []secretMetadata `json:"items"`
	}](t, send(http.MethodGet, hostfixtures.SecretsPath, ""), http.StatusOK)
	if len(list.Items) != 2 || list.Items[0].Name != "A_TOKEN" || list.Items[0].Status != "available" || list.Items[1].Name != "OLD_TOKEN" || list.Items[1].Status != "expired" {
		t.Fatal("shared inventory did not sort metadata and project expiry")
	}
	old := decodeSecretResponse[secretMetadata](t, send(http.MethodGet, hostfixtures.SecretsPath+"/OLD_TOKEN", ""), http.StatusOK)
	if old.Status != "expired" || !old.CreatedAt.Equal(created) {
		t.Fatal("expired secret metadata was changed or timestamp precision was lost")
	}
	createdRequestName := decodeSecretResponse[secretMetadata](t, send(http.MethodPost, hostfixtures.SecretsPath, `{"name":"requests","value":""}`), http.StatusCreated)
	fetchedRequestName := decodeSecretResponse[secretMetadata](t, send(http.MethodGet, hostfixtures.SecretsPath+"/requests", ""), http.StatusOK)
	if createdRequestName.ID != fetchedRequestName.ID || fetchedRequestName.Name != "requests" {
		t.Fatal("secret named requests collided with pending request inventory")
	}
	replacedRequestName := decodeSecretResponse[secretMetadata](t, send(http.MethodPut, hostfixtures.SecretsPath+"/requests", fmt.Sprintf(`{"id":%q,"version":1,"value":""}`, createdRequestName.ID)), http.StatusOK)
	if replacedRequestName.ID != createdRequestName.ID || replacedRequestName.Version != 2 {
		t.Fatal("secret named requests did not retain normal replacement semantics")
	}
	deletedRequestName := send(http.MethodDelete, fmt.Sprintf("%s/requests?id=%s&version=2", hostfixtures.SecretsPath, replacedRequestName.ID), "")
	if deletedRequestName.Status != http.StatusNoContent {
		t.Fatal("secret named requests could not be deleted")
	}
	assertSecretError(t, send(http.MethodGet, hostfixtures.SecretsPath+"/requests", ""), http.StatusNotFound, "secret_not_found")
	empty := decodeSecretResponse[struct {
		Items []pendingSecretView `json:"items"`
	}](t, send(http.MethodGet, secretRequestsPath, ""), http.StatusOK)
	if empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal("pending request inventory did not remain separate from secret metadata")
	}
	assertSecretError(t, send(http.MethodPost, hostfixtures.SecretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":""}`), http.StatusBadRequest, "invalid_request")
	assertSecretError(t, send(http.MethodGet, hostfixtures.SecretsPath+"?agent_type=builder-agent", ""), http.StatusBadRequest, "invalid_request")
	if _, err := store.Get(t.Context(), expired.Metadata.Key); err != nil {
		t.Fatal("inventory deleted the expired record")
	}
	renewed := decodeSecretResponse[secretMetadata](t, send(http.MethodPut, hostfixtures.SecretsPath+"/OLD_TOKEN", fmt.Sprintf(`{"id":%q,"version":1,"value":""}`, old.ID)), http.StatusOK)
	if renewed.ExpiresAt != nil || renewed.Status != "available" || renewed.Version != 2 {
		t.Fatal("replacement with omitted expiry did not clear the previous expiration")
	}
}

func TestSecretRoutesRequireAuthenticationAndExplicitEncryptionKey(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, hostfixtures.SecretsPath},
		{http.MethodPost, hostfixtures.SecretsPath}, {http.MethodGet, hostfixtures.SecretPath},
		{http.MethodPut, hostfixtures.SecretPath}, {http.MethodDelete, hostfixtures.SecretPath},
	} {
		assertSecretError(t, server.Request(t, route.method, route.path), http.StatusUnauthorized, "authentication_required")
		assertSecretError(t, server.Request(t, route.method, route.path, hostfixtures.Authenticate), http.StatusServiceUnavailable, "secrets_unavailable")
	}
	assertHealthy(t, server.Request(t, http.MethodGet, "/readyz"))
	if server.Request(t, http.MethodGet, "/api/v1/agents", hostfixtures.Authenticate).Status != http.StatusOK {
		t.Fatal("missing optional key disabled existing API routes")
	}
	if _, err := os.Stat(filepath.Join(fixture.Root, "database", "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("disabled secrets opened or created a secret database")
	}
}

func TestSecretRequestsRejectMalformedAndAmbiguousInput(t *testing.T) {
	fixture := hostfixtures.New(t)
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	server := hostfixtures.Start(t, fixture.LocalArgs())
	for _, tc := range []struct{ name, method, path, body string }{
		{"missing value", "POST", hostfixtures.SecretsPath, `{"name":"TOKEN"}`},
		{"null value", "POST", hostfixtures.SecretsPath, `{"name":"TOKEN","value":null}`},
		{"nonstring value", "POST", hostfixtures.SecretsPath, `{"name":"TOKEN","value":1}`},
		{"unknown field", "POST", hostfixtures.SecretsPath, `{"name":"TOKEN","value":"","ciphertext":"private-value"}`},
		{"obsolete profile", "POST", hostfixtures.SecretsPath, `{"profile":"default","name":"TOKEN","value":""}`},
		{"multiple bodies", "POST", hostfixtures.SecretsPath, `{"name":"TOKEN","value":""}{}`},
		{"empty body", "POST", hostfixtures.SecretsPath, ""},
		{"null body", "POST", hostfixtures.SecretsPath, "null"},
		{"obsolete scope", "POST", hostfixtures.SecretsPath, `{"agent_type":"builder-agent","name":"TOKEN","value":""}`},
		{"invalid name", "POST", hostfixtures.SecretsPath, `{"name":"../TOKEN","value":""}`},
		{"past expiry", "POST", hostfixtures.SecretsPath, `{"name":"TOKEN","value":"","expires_at":"2000-01-01T00:00:00Z"}`},
		{"zero expiry", "POST", hostfixtures.SecretsPath, `{"name":"TOKEN","value":"","expires_at":"0001-01-01T00:00:00Z"}`},
		{"unknown list query", "GET", hostfixtures.SecretsPath + "?agent_type=builder-agent&value=private-query-value", ""},
		{"obsolete scope", "GET", hostfixtures.SecretsPath + "?agent_type=builder-agent", ""},
		{"duplicated scope", "GET", hostfixtures.SecretsPath + "?agent_type=builder-agent&agent_type=web-agent", ""},
		{"obsolete profile query", "GET", hostfixtures.SecretsPath + "?agent_type=builder-agent&profile=default", ""},
		{"delete missing precondition", "DELETE", hostfixtures.SecretPath, ""},
		{"delete malformed id", "DELETE", hostfixtures.SecretPath + "?id=private-invalid-id&version=1", ""},
		{"delete zero version", "DELETE", hostfixtures.SecretPath + "?id=00000000-0000-4000-8000-000000000001&version=0", ""},
		{"delete duplicate version", "DELETE", hostfixtures.SecretPath + "?id=00000000-0000-4000-8000-000000000001&version=1&version=2", ""},
		{"replace missing identity", "PUT", hostfixtures.SecretPath, `{"version":1,"value":""}`},
		{"replace missing value", "PUT", hostfixtures.SecretPath, `{"id":"00000000-0000-4000-8000-000000000001","version":1}`},
		{"replace obsolete profile", "PUT", hostfixtures.SecretPath, `{"id":"00000000-0000-4000-8000-000000000001","version":1,"value":"","profile":"default"}`},
		{"replace zero expiry", "PUT", hostfixtures.SecretPath, `{"id":"00000000-0000-4000-8000-000000000001","version":1,"value":"","expires_at":"0001-01-01T00:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertSecretError(t, secretRequest(t, server, tc.method, tc.path, tc.body), http.StatusBadRequest, "invalid_request")
		})
	}
	for _, contentType := range []string{"", "text/plain"} {
		result := server.Request(t, http.MethodPost, hostfixtures.SecretsPath, hostfixtures.Authenticate, hostfixtures.SecretBody(`{"name":"TOKEN","value":""}`, contentType))
		assertSecretError(t, result, http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	large := `{"name":"TOKEN","value":"` + strings.Repeat("x", 1<<20) + `"}`
	assertSecretError(t, secretRequest(t, server, http.MethodPost, hostfixtures.SecretsPath, large), http.StatusRequestEntityTooLarge, "request_too_large")
	list := decodeSecretResponse[struct {
		Items []secretMetadata `json:"items"`
	}](t, secretRequest(t, server, http.MethodGet, hostfixtures.SecretsPath, ""), http.StatusOK)
	if len(list.Items) != 0 {
		t.Fatal("rejected request mutated the secret inventory")
	}
}

func TestSecretEncryptionKeyFailsBeforeStartupAndNeverAppearsInHelp(t *testing.T) {
	fixture := hostfixtures.New(t)
	const invalid = "private-invalid-encryption-key"
	t.Setenv("SECRETS_ENCRYPTION_KEY", invalid)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	var diagnostic bytes.Buffer
	ready := false
	err := cli.RunWithOptions(ctx, fixture.LocalArgs(), &diagnostic, cli.Options{Ready: func(net.Addr) { ready = true }})
	if err == nil || ctx.Err() != nil || ready {
		t.Fatal("invalid encryption key did not fail before serving requests")
	}
	if !strings.Contains(err.Error(), "SECRETS_ENCRYPTION_KEY") || strings.Contains(err.Error()+diagnostic.String(), invalid) {
		t.Fatal("invalid key diagnostic must name its environment variable without its value")
	}
	if _, err := os.Stat(filepath.Join(fixture.Root, "database")); !os.IsNotExist(err) {
		t.Fatal("invalid encryption key opened the database")
	}
	t.Setenv("SECRETS_ENCRYPTION_KEY", hostfixtures.SecretTestKey)
	diagnostic.Reset()
	if code := cli.Main([]string{"--help"}, &diagnostic, &diagnostic); code != 0 {
		t.Fatal("help failed with configured secret encryption")
	}
	if strings.Contains(diagnostic.String(), hostfixtures.SecretTestKey) {
		t.Fatal("help exposed the encryption key")
	}
}

// Provider failures are exercised at the HTTP boundary without corrupting a
// real database or making uncertain outcomes depend on filesystem timing.
func TestSecretFailuresAreSanitizedAndNeverRetried(t *testing.T) {
	cipher, err := crypto.NewSecretCipher(hostfixtures.SecretTestKey)
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
		{"internal_error", 500, errors.New(hostfixtures.SecretTestValue + " private backend failure")},
	} {
		t.Run(tc.code, func(t *testing.T) {
			store := fakes.NewFailedSecretCreate(tc.err)
			service, err := secretservice.NewService(store, cipher)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			secretendpoints.Register(mux, service)
			request := httptest.NewRequest(http.MethodPost, hostfixtures.SecretsPath, strings.NewReader(`{"name":"TOKEN","value":"`+hostfixtures.SecretTestValue+`"}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			mux.ServeHTTP(recorder, request)

			assertSecretError(t, hostfixtures.Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}, tc.status, tc.code)
			calls := store.Snapshot()
			if calls.Attempts != 1 || calls.Remaining <= 0 || calls.Remaining > 10*time.Second {
				t.Fatal("secret mutation was retried or lost its bounded request context")
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("failure response may be cached")
			}
		})
	}
}

func TestSecretOpenAPIDescribesWriteOnlyValuesAndMetadata(t *testing.T) {
	fixture := hostfixtures.New(t)
	server := hostfixtures.Start(t, append(fixture.LocalArgs(), "--swagger=true"))
	result := server.Request(t, http.MethodGet, "/openapi.json", hostfixtures.Authenticate)
	var document struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if result.Status != http.StatusOK || json.Unmarshal([]byte(result.Body), &document) != nil {
		t.Fatal("could not read API documentation")
	}
	if strings.Contains(result.Body, `"profile"`) || strings.Contains(result.Body, `"secret_profile"`) || strings.Contains(result.Body, "{profile}") {
		t.Fatal("API documentation still exposes secret profiles")
	}
	for path, methods := range map[string][]string{
		hostfixtures.SecretsPath:             {"get", "post"},
		hostfixtures.SecretsPath + "/{name}": {"get", "put", "delete"},
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
	for _, name := range []string{"Secret", "CreateSecretRequest"} {
		if _, present := properties(name)["agent_type"]; present {
			t.Errorf("shared secret schema %s still contains an agent-type scope", name)
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

func assertSecretError(t *testing.T, result hostfixtures.Response, status int, code string) {
	t.Helper()
	assertNoSecretResponse(t, result)
	var payload map[string]string
	if result.Status != status || json.Unmarshal([]byte(result.Body), &payload) != nil || len(payload) != 1 || payload["error"] != code {
		t.Fatalf("secret API returned %d, want %d with error %q", result.Status, status, code)
	}
}

func assertSecretOnDisk(t *testing.T, root string, expected secretMetadata, plaintext string) {
	t.Helper()
	store, err := fssecrets.NewSecretFsStore(filepath.Join(root, "database"))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(t.Context(), secretmodel.Key{Name: expected.Name})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewSecretCipher(hostfixtures.SecretTestKey)
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
		for _, private := range []string{hostfixtures.SecretTestKey, hostfixtures.SecretTestValue, hostfixtures.SecretTestReplacement} {
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
