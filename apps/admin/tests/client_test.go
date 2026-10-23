package tests

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/framework/host"
)

func TestAPIConnectionKeepsAuthenticationAndProxyPrefix(t *testing.T) {
	var requests atomic.Int32
	missionPath := "/api/v1/missions/" + url.PathEscape("review/site%new\\draft")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		username, password, ok := r.BasicAuth()
		if !ok || username != "operator" || password != "private-password" {
			t.Error("API request did not use the configured credentials")
		}
		if r.URL.RequestURI() != "/commander/api/v1/missions?limit=2" && r.URL.RequestURI() != "/commander"+missionPath || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected API request: path=%q accept=%q", r.URL.RequestURI(), r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	connection := client.Connection{URL: server.URL + "/commander/", Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}}
	api, err := client.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	data, err := api.Request(context.Background(), http.MethodGet, "/api/v1/missions?limit=2", nil)
	if err != nil || string(data) != `{"items":[]}` || requests.Load() != 1 {
		t.Fatalf("API request: body=%q error=%v requests=%d", data, err, requests.Load())
	}
	if _, err := api.Request(context.Background(), http.MethodGet, missionPath, nil); err != nil || requests.Load() != 2 {
		t.Fatalf("escaped mission name was not preserved: error=%v requests=%d", err, requests.Load())
	}
	for _, diagnostic := range []string{fmt.Sprintf("%v %+v %#v", connection, connection, connection), fmt.Sprintf("%v %+v %#v", api, api, api)} {
		if strings.Contains(diagnostic, "operator") || strings.Contains(diagnostic, "private-password") {
			t.Fatal("API diagnostics exposed credentials")
		}
	}
	for _, endpoint := range []string{"https://operator:private-password@example.com", "https://example.com?token=private-password", "https://example.com#private-password", "https://example.com#", "file:///state"} {
		_, err := client.New(client.Connection{URL: endpoint, Credentials: connection.Credentials})
		if err == nil || strings.Contains(err.Error(), "private-password") {
			t.Fatalf("invalid API URL was accepted or disclosed: %v", err)
		}
	}
}

func TestAPIRejectsRedirectsAndPathsOutsideItsPrefix(t *testing.T) {
	var redirected, requests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, destination.URL+"/private-password", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	api := newAPIClient(t, server.URL)
	_, err := api.Request(context.Background(), http.MethodGet, "/api/v1/missions", nil)
	var status *client.StatusError
	if !errors.As(err, &status) || status.StatusCode != http.StatusTemporaryRedirect || redirected.Load() != 0 || strings.Contains(err.Error(), "private-password") {
		t.Fatalf("redirect was followed or exposed: error=%v redirected=%d", err, redirected.Load())
	}
	for _, path := range []string{
		destination.URL + "/api/v1/missions", "//example.com/api/v1/missions", "/other",
		"/api/v1/../credentials", "/api/v1/./credentials", "/api/v1/\\credentials", "/api/v1//credentials", "/api/v1/missions#fragment",
	} {
		if _, err := api.Request(context.Background(), http.MethodGet, path, nil); err == nil {
			t.Errorf("accepted unsafe path %q", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := api.Request(ctx, http.MethodPost, "/api/v1/missions", []byte(`{}`)); !errors.Is(err, context.Canceled) || errors.Is(err, client.ErrWriteUncertain) {
		t.Fatalf("pre-canceled request has an uncertain outcome: %v", err)
	}
	if requests.Load() != 1 || redirected.Load() != 0 {
		t.Fatal("rejected API requests reached a server")
	}
}

func TestAPIHandlesBoundedResponsesAndUncertainWritesWithoutRetries(t *testing.T) {
	for _, test := range []struct {
		name       string
		method     string
		status     int
		body       string
		shortRead  bool
		disconnect bool
		wantError  error
		uncertain  bool
		statusCode string
	}{
		{name: "oversized read", method: http.MethodGet, status: 200, body: strings.Repeat(" ", 4<<20+1), wantError: client.ErrResponseTooLarge},
		{name: "oversized write response", method: http.MethodPost, status: 201, body: strings.Repeat(" ", 4<<20+1), wantError: client.ErrResponseTooLarge, uncertain: true},
		{name: "truncated write response", method: http.MethodPut, status: 200, body: `{}`, shortRead: true, uncertain: true},
		{name: "lost write acknowledgement", method: http.MethodPost, status: 201, disconnect: true, uncertain: true},
		{name: "invalid write response", method: http.MethodPost, status: 201, body: `private-password`, wantError: client.ErrInvalidResponse, uncertain: true},
		{name: "server failure after write", method: http.MethodDelete, status: 503, body: `{"error":"secret_write_uncertain"}`, uncertain: true, statusCode: "secret_write_uncertain"},
		{name: "authentication", method: http.MethodPost, status: 401, body: `{"error":"unauthorized"}`, wantError: client.ErrAuthentication, statusCode: "unauthorized"},
		{name: "conflict", method: http.MethodPost, status: 409, body: `{"error":"mission_exists"}`, statusCode: "mission_exists"},
		{name: "untrusted error", method: http.MethodGet, status: 400, body: `{"error":"private-password: bad request"}`},
		{name: "oversized error", method: http.MethodGet, status: 400, body: strings.Repeat("private-password", 5000)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if test.disconnect {
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error("could not simulate lost acknowledgement")
						return
					}
					_ = connection.Close()
					return
				}
				if test.shortRead {
					w.Header().Set("Content-Length", "100")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			api := newAPIClient(t, server.URL)
			_, err := api.Request(context.Background(), test.method, "/api/v1/missions", []byte(`{}`))
			if err == nil || requests.Load() != 1 {
				t.Fatalf("failed request was accepted or retried: error=%v requests=%d", err, requests.Load())
			}
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Errorf("expected %v; got %v", test.wantError, err)
			}
			if errors.Is(err, client.ErrWriteUncertain) != test.uncertain {
				t.Errorf("unexpected write certainty: %v", err)
			}
			if test.status < 200 || test.status >= 300 {
				var status *client.StatusError
				if !errors.As(err, &status) || status.StatusCode != test.status || status.Code != test.statusCode {
					t.Errorf("unexpected API status error: %v", err)
				}
			}
			if strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("error exposed the API URL or server response")
			}
		})
	}
}

func newAPIClient(t *testing.T, endpoint string) *client.Client {
	t.Helper()
	api, err := client.New(client.Connection{URL: endpoint, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	return api
}
