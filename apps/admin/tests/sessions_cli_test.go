package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/cli"
	"github.com/defos-ai/commander/apps/admin/client"
	adminsessions "github.com/defos-ai/commander/apps/admin/sessions"
	"github.com/defos-ai/commander/framework/host"
)

const (
	cliSessionID = "c4712934-2a88-44e0-8d97-02974966b215"
	cliScopeID   = "d4712934-2a88-44e0-8d97-02974966b215"
	cliRequestID = "e4712934-2a88-44e0-8d97-02974966b215"
	cliOwnerID   = "fixture-agent-00000001"
)

func TestSessionCLIReadsAndControlsSavedIdentity(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	saved := sessionCLIFixture()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		username, password, ok := r.BasicAuth()
		if !ok || username != "operator" || password != "private-password" {
			t.Error("session command did not use its configured API login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions":
			if r.URL.Query().Get("scope_id") != cliScopeID || r.URL.Query().Get("owner_agent_id") != cliOwnerID {
				t.Error("session list dropped intersecting filters")
			}
			json.NewEncoder(w).Encode(map[string]any{"items": []adminsessions.Session{saved}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/"+cliSessionID:
			json.NewEncoder(w).Encode(saved)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/control":
			var input struct {
				ID           string `json:"id"`
				ScopeID      string `json:"scope_id"`
				OwnerAgentID string `json:"owner_agent_id"`
				Method       string `json:"method"`
				RequestID    string `json:"request_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.ID != cliSessionID || input.ScopeID != cliScopeID || input.OwnerAgentID != cliOwnerID || input.RequestID != cliRequestID {
				t.Error("control did not preserve saved identity and supplied request ID")
			}
			methods = append(methods, input.Method)
			switch input.Method {
			case "verify":
				saved.State = "connected"
			case "close":
				saved.Disposition = "closing"
			case "abandon":
				saved.State, saved.Disposition, saved.ReleaseReason = "lost", "released", "abandoned"
			}
			json.NewEncoder(w).Encode(saved)
		default:
			t.Errorf("unexpected session request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	connection := client.Connection{URL: server.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}}
	output, err := runSessionCLI(t, connection, "--scope-id", cliScopeID, "sessions", "list", "--json", "--owner-agent-id", cliOwnerID)
	var listed struct{ Items []adminsessions.Session }
	if err != nil || json.Unmarshal(output, &listed) != nil || len(listed.Items) != 1 || listed.Items[0].ID != cliSessionID {
		t.Fatalf("filtered list failed: %s, %v", output, err)
	}
	output, err = runSessionCLI(t, connection, "sessions", "inspect", "--json", cliSessionID)
	var detail adminsessions.Session
	if err != nil || json.Unmarshal(output, &detail) != nil || detail.ScopeID != cliScopeID || detail.OwnerAgentID != cliOwnerID {
		t.Fatalf("ID inspection lost its scope/owner: %s, %v", output, err)
	}
	for _, method := range []string{"verify", "close", "abandon"} {
		if method == "abandon" {
			mu.Lock()
			saved.State = "lost"
			mu.Unlock()
		}
		args := []string{"sessions", method, cliSessionID, "--request-id", cliRequestID}
		if method != "abandon" {
			args = append(args, "--json")
		}
		output, err = runSessionCLI(t, connection, args...)
		if err != nil {
			t.Fatalf("%s failed: %v", method, err)
		}
		if method == "abandon" {
			if !bytes.Contains(output, []byte("lost")) || !bytes.Contains(output, []byte("released")) || !bytes.Contains(output, []byte("closure is unconfirmed")) || !bytes.Contains(output, []byte(cliRequestID)) {
				t.Fatalf("abandonment output lost uncertainty: %s", output)
			}
			continue
		}
		var result adminsessions.ControlResult
		if json.Unmarshal(output, &result) != nil || result.RequestID != cliRequestID || result.Session.ID != cliSessionID || method == "close" && (result.Session.Disposition != "closing" || result.Session.State != "connected") {
			t.Fatalf("control output claimed an unobserved result: %s", output)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(methods, []string{"verify", "close", "abandon"}) {
		t.Fatalf("controls were omitted or retried: %v", methods)
	}
}

func TestSessionCLIRejectsArgumentsBeforeAPIAndKeepsHelpOffline(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	connection := client.Connection{URL: server.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}}
	for _, args := range [][]string{
		{"sessions", "unknown"}, {"sessions", "inspect", "invalid"},
		{"sessions", "list", "--scope-id="}, {"sessions", "list", "--owner-agent-id="},
		{"sessions", "list", "--scope-id=invalid"}, {"sessions", "list", "--owner-agent-id=fixture"},
		{"sessions", "list", "--request-id", cliRequestID}, {"sessions", "list", "--dry-run"},
		{"sessions", "inspect", cliSessionID, "--request-id", cliRequestID},
		{"sessions", "close", cliSessionID, "--scope-id", cliScopeID},
		{"sessions", "close", cliSessionID, "--request-id="}, {"sessions", "close", cliSessionID, "--request-id=invalid"},
		{"missions", "list", "--scope-id", cliScopeID}, {"secrets", "requests", "--request-id", cliRequestID},
	} {
		output, err := runSessionCLI(t, connection, args...)
		var usage *cli.UsageError
		if !errors.As(err, &usage) || len(output) != 0 {
			t.Fatalf("invalid session arguments %v: output=%s error=%v", args, output, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid session input reached the API")
	}
	connection.URL = ":invalid"
	for _, args := range [][]string{{"sessions"}, {"sessions", "--help"}, {"sessions", "list", "--help"}, {"help", "sessions"}} {
		output, err := runSessionCLI(t, connection, args...)
		if err != nil || !strings.Contains(string(output), "Usage: commander-admin sessions") {
			t.Fatalf("offline session help %v: %s, %v", args, output, err)
		}
	}
}

func runSessionCLI(t *testing.T, connection client.Connection, args ...string) ([]byte, error) {
	t.Helper()
	var output bytes.Buffer
	err := cli.RunWithOptions(t.Context(), args, nil, &output, &output, cli.Options{Connection: &connection})
	return output.Bytes(), err
}

func sessionCLIFixture() adminsessions.Session {
	now := time.Now().UTC()
	return adminsessions.Session{ID: cliSessionID, ScopeID: cliScopeID, OwnerAgentID: cliOwnerID, Kind: "fixture", Label: "Test session", State: "connected", Disposition: "active", Revision: 1, CreatedAt: now, UpdatedAt: now}
}
