package host

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	secretendpoints "github.com/defos-ai/commander/apps/adapters/api/endpoints/secrets"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
	fssecrets "github.com/defos-ai/commander/providers/fs/secrets"
)

func PendingRequestRecord(name string) secretmodel.RequestRecord {
	now := time.Now().UTC()
	return secretmodel.RequestRecord{Request: secretmodel.Request{
		ID: id.New(), AgentID: "builder-agent-01234567", AgentType: "builder-agent", Key: secretmodel.Key{Name: name},
		Status: secretmodel.Pending, RequestedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}}
}

func PendingRequestService(t *testing.T, store secretmodel.SecretStore) *secretservice.Service {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(SecretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	service, err := secretservice.NewService(store, cipher)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func PendingRequestResponse(ctx context.Context, service *secretservice.Service, requests secretmodel.RequestStore) Response {
	mux := http.NewServeMux()
	secretendpoints.RegisterRequests(mux, service, requests)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/secret-requests", nil))
	return Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
}

func SecretEndpoints(service *secretservice.Service, requests secretmodel.RequestStore) http.Handler {
	mux := http.NewServeMux()
	secretendpoints.Register(mux, service)
	secretendpoints.RegisterRequests(mux, service, requests)
	return mux
}

func SecretEndpointResponse(ctx context.Context, handler http.Handler, method, path, body string) Response {
	request := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return Response{Status: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
}

func SecretRequestStores(t *testing.T, fixture Fixture) (secretmodel.RequestStore, secretmodel.SecretStore) {
	t.Helper()
	path := filepath.Join(fixture.Root, "database")
	requests, err := fssecrets.NewRequestFsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	values, err := fssecrets.NewSecretFsStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return requests, values
}

func PendingSecretRequest(agentType, name, agentID string) secretmodel.Request {
	now := time.Now().UTC()
	return secretmodel.Request{
		ID: id.New(), Key: secretmodel.Key{Name: name},
		AgentID: agentID, AgentType: agentType, Status: secretmodel.Pending, RequestedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
}

func ReserveSecretDelivery(t *testing.T, requests secretmodel.RequestStore, values secretmodel.SecretStore, request secretmodel.Request) secretmodel.Delivery {
	t.Helper()
	secret, err := values.Get(t.Context(), request.Key)
	if err != nil {
		t.Fatal(err)
	}
	deadline := request.ExpiresAt
	if expiry := secret.Metadata.ExpiresAt; !expiry.IsZero() && expiry.Before(deadline) {
		deadline = expiry
	}
	delivery := secretmodel.Delivery{
		ID: id.New(), RequestID: request.ID, AgentID: request.AgentID, Secret: secret,
		CreatedAt: time.Now().UTC(), ExpiresAt: deadline,
	}
	if err := requests.SaveDelivery(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	return delivery
}

const (
	SecretTestKey         = "014ac583269fe047014ac583269fe047014ac583269fe047014ac583269fe047"
	SecretTestValue       = "host-test-private-token-original"
	SecretTestReplacement = "host-test-private-token-replaced"
	SecretsPath           = "/api/v1/secrets"
	SecretPath            = SecretsPath + "/GITHUB_TOKEN"
)

func SecretBody(body, contentType string) func(*http.Request) {
	return func(request *http.Request) {
		request.Body = io.NopCloser(strings.NewReader(body))
		request.ContentLength = int64(len(body))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
	}
}

func Authenticate(request *http.Request) {
	request.SetBasicAuth("api-fixture-user", "api-fixture-password")
}
