package fixtures

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/framework/host"
)

func StartAPI(t testing.TB, handler http.Handler) client.Connection {
	t.Helper()
	credentials := host.BasicCredentials{Username: "operator", Password: "private-password"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != credentials.Username || password != credentials.Password {
			t.Error("command did not authenticate its API request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return client.Connection{URL: server.URL, Credentials: credentials}
}
