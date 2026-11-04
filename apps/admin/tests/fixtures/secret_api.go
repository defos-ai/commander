package fixtures

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/admin/secrets"
	"github.com/defos-ai/commander/framework/host"
)

func SecretAPI(t testing.TB, handler http.Handler) *client.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "operator" || password != "local-test-password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	api, err := client.New(client.Connection{
		URL: server.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "local-test-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	return api
}

func TransientSecretRequest() secrets.TransientRequest {
	now := time.Now().UTC()
	return secrets.TransientRequest{
		ID: "00000000-0000-4000-8000-000000000001", Type: "transient", Name: "VERIFICATION_CODE",
		AgentType: "browser-agent", AgentID: "browser-agent-00000001",
		OperationID: "00000000-0000-4000-8000-000000000002",
		TaskID:      "00000000-0000-4000-8000-000000000003",
		CommandID:   "00000000-0000-4000-8000-000000000004",
		RequestedAt: now, ExpiresAt: now.Add(time.Minute), Status: "pending",
	}
}
