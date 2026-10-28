package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/admin/cli"
	"github.com/defos-ai/commander/apps/admin/client"
	adminsessions "github.com/defos-ai/commander/apps/admin/sessions"
	"github.com/defos-ai/commander/apps/admin/tests/fakes"
	"github.com/defos-ai/commander/apps/admin/tests/fixtures"
	"github.com/defos-ai/commander/framework/host"
)

func TestSessionCLIReadsAndControlsSavedIdentity(t *testing.T) {
	api := fakes.NewSessionAPI(t, fakes.SessionAPIConfig{Session: fixtures.Session(), RequestID: fixtures.SessionRequestID})
	connection := fixtures.StartAPI(t, api)
	output, err := runSessionCLI(t, connection, "--scope-id", fixtures.SessionScopeID, "sessions", "list", "--json", "--owner-agent-id", fixtures.SessionOwnerID)
	var listed struct{ Items []adminsessions.Session }
	if err != nil || json.Unmarshal(output, &listed) != nil || len(listed.Items) != 1 || listed.Items[0].ID != fixtures.SessionID {
		t.Fatalf("filtered list failed: %s, %v", output, err)
	}
	output, err = runSessionCLI(t, connection, "sessions", "inspect", "--json", fixtures.SessionID)
	var detail adminsessions.Session
	if err != nil || json.Unmarshal(output, &detail) != nil || detail.ScopeID != fixtures.SessionScopeID || detail.OwnerAgentID != fixtures.SessionOwnerID {
		t.Fatalf("ID inspection lost its scope/owner: %s, %v", output, err)
	}
	for _, method := range []string{"verify", "close", "abandon"} {
		if method == "abandon" {
			api.SetState("lost")
		}
		args := []string{"sessions", method, fixtures.SessionID, "--request-id", fixtures.SessionRequestID}
		if method != "abandon" {
			args = append(args, "--json")
		}
		output, err = runSessionCLI(t, connection, args...)
		if err != nil {
			t.Fatalf("%s failed: %v", method, err)
		}
		if method == "abandon" {
			if !bytes.Contains(output, []byte("lost")) || !bytes.Contains(output, []byte("released")) || !bytes.Contains(output, []byte("closure is unconfirmed")) || !bytes.Contains(output, []byte(fixtures.SessionRequestID)) {
				t.Fatalf("abandonment output lost uncertainty: %s", output)
			}
			continue
		}
		var result adminsessions.ControlResult
		if json.Unmarshal(output, &result) != nil || result.RequestID != fixtures.SessionRequestID || result.Session.ID != fixtures.SessionID || method == "close" && (result.Session.Disposition != "closing" || result.Session.State != "connected") {
			t.Fatalf("control output claimed an unobserved result: %s", output)
		}
	}
	if methods := api.Snapshot().Methods; !reflect.DeepEqual(methods, []string{"verify", "close", "abandon"}) {
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
		{"sessions", "list", "--request-id", fixtures.SessionRequestID}, {"sessions", "list", "--dry-run"},
		{"sessions", "inspect", fixtures.SessionID, "--request-id", fixtures.SessionRequestID},
		{"sessions", "close", fixtures.SessionID, "--scope-id", fixtures.SessionScopeID},
		{"sessions", "close", fixtures.SessionID, "--request-id="}, {"sessions", "close", fixtures.SessionID, "--request-id=invalid"},
		{"missions", "list", "--scope-id", fixtures.SessionScopeID}, {"secrets", "requests", "--request-id", fixtures.SessionRequestID},
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
