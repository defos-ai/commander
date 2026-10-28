package tests

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/cli"
	"github.com/defos-ai/commander/apps/admin/client"
	adminsecrets "github.com/defos-ai/commander/apps/admin/secrets"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/host"
)

const secretTestID = "da378c90-63eb-46da-b186-552e1e144510"

func TestSecretCommandsPreserveValuesAndVersionedMutations(t *testing.T) {
	fixture := newSecretAPI(t)
	const value = "secret-value-must-not-be-printed\nwith a trailing newline\n"
	for _, current := range []string{value, "", "renewed-secret-value"} {
		var input io.Reader = strings.NewReader(current)
		var written <-chan error
		if current == value {
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			finished := make(chan struct{})
			t.Cleanup(func() {
				writer.Close()
				reader.Close()
				<-finished
			})
			written = result
			go func() {
				defer close(finished)
				defer writer.Close()
				select {
				case <-fixture.metadataRead:
					_, err := io.WriteString(writer, current)
					result <- err
				case <-t.Context().Done():
					result <- t.Context().Err()
				}
			}()
			input = reader
		} else if current == "" {
			file, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { file.Close() })
			input = file
		}
		output, err := secretCommand(t, fixture.connection(), input, "secrets", "set", "builder-agent", "GITHUB_TOKEN", "--json")
		if err != nil {
			t.Fatalf("set secret: %v", err)
		}
		if written != nil {
			if err := <-written; err != nil {
				t.Fatalf("write piped secret: %v", err)
			}
		}
		var metadata adminsecrets.Metadata
		if json.Unmarshal(output, &metadata) != nil || metadata.ID != secretTestID || metadata.Version == 0 || metadata.Status != "available" || strings.Contains(string(output), value) || strings.Contains(string(output), "renewed-secret-value") {
			t.Fatal("set did not return valid metadata only")
		}
		fixture.mu.Lock()
		if fixture.values[len(fixture.values)-1] != current {
			t.Error("stdin secret bytes changed")
		}
		version := fixture.saved.Version
		if version < 3 {
			expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			if version == 2 {
				expiry = time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
				fixture.saved.Status = "expired"
			}
			fixture.saved.ExpiresAt = &expiry
		} else if fixture.saved.ExpiresAt != nil {
			t.Error("renewing an expired secret retained its expired timestamp")
		}
		fixture.mu.Unlock()
	}
	for _, args := range [][]string{
		{"secrets", "list", "builder-agent", "--json"},
		{"secrets", "requests", "builder-agent", "--json"},
	} {
		output, err := secretCommand(t, fixture.connection(), nil, args...)
		var result struct {
			Items []json.RawMessage `json:"items"`
		}
		if err != nil || json.Unmarshal(output, &result) != nil || len(result.Items) != 1 || bytes.Contains(output, []byte(`"value"`)) || bytes.Contains(output, []byte("server-private-value")) {
			t.Fatalf("list must return metadata only: %v", err)
		}
	}
	if _, err := secretCommand(t, fixture.connection(), nil, "secrets", "delete", "builder-agent", "GITHUB_TOKEN", "--json"); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.saved != nil || !reflect.DeepEqual(fixture.requests, []string{"GET secret", "POST", "GET secret", "PUT", "GET secret", "PUT", "GET list", "GET requests", "GET secret", "DELETE"}) {
		t.Fatalf("mutations skipped version reads or were retried: %v", fixture.requests)
	}
}

func TestSecretCommandsRejectUnsafeInputBeforeMutation(t *testing.T) {
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"route_not_found"}`)
	}))
	defer server.Close()
	connection := client.Connection{URL: server.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}}
	reader := secretReader{reads: &reads}
	_, err := secretCommand(t, connection, reader, "secrets", "set", "builder-agent", "GITHUB_TOKEN")
	if err == nil || reads.Load() != 0 || writes.Load() != 0 {
		t.Fatal("an unrelated 404 prompted for a value or created a secret")
	}
	fixture := newSecretAPI(t)
	for _, args := range [][]string{
		{"secrets", "set", "builder-agent", "GITHUB_TOKEN", "private-positional-value"},
		{"secrets", "set", "builder-agent", "GITHUB_TOKEN", "--value", "private-flag-value"},
	} {
		output, err := secretCommand(t, fixture.connection(), reader, args...)
		var usage *cli.UsageError
		if !errors.As(err, &usage) || reads.Load() != 0 || strings.Contains(err.Error()+string(output), "private-") {
			t.Fatal("a secret argument was accepted, read stdin or exposed a value")
		}
	}
	for _, value := range []string{strings.Repeat("x", 64<<10+1), string([]byte{0xff})} {
		if _, err := secretCommand(t, fixture.connection(), strings.NewReader(value), "secrets", "set", "builder-agent", "GITHUB_TOKEN"); err == nil {
			t.Error("unsafe or oversized stdin secret was accepted")
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.values) != 0 {
		t.Fatal("invalid command input submitted a secret")
	}
}

func TestSecretWritesValidateMetadataWithoutRetrying(t *testing.T) {
	const marker = "private-secret-never-display"
	valid := `{"id":"` + secretTestID + `","agent_type":"builder-agent","name":"GITHUB_TOKEN","version":2,"status":"available"}`
	key := secretmodel.Key{Scope: secretmodel.Scope{AgentType: "builder-agent"}, Name: "GITHUB_TOKEN"}
	expected := adminsecrets.Metadata{ID: secretTestID, AgentType: key.Scope.AgentType, Name: key.Name, Version: 1, Status: "available"}
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"concurrent rotation", 409, `{"error":"secret_conflict"}`, adminsecrets.ErrConflict},
		{"uncertain save", 503, `{"error":"secret_write_uncertain"}`, client.ErrWriteUncertain},
		{"wrong scope", 200, strings.Replace(valid, "builder-agent", "other-agent", 1), client.ErrWriteUncertain},
		{"wrong name", 200, strings.Replace(valid, "GITHUB_TOKEN", "OTHER_TOKEN", 1), client.ErrWriteUncertain},
		{"invalid identity", 200, strings.Replace(valid, secretTestID, marker, 1), client.ErrWriteUncertain},
		{"changed identity", 200, strings.Replace(valid, secretTestID, "5123e0e2-d5e1-41d0-a321-5cce05fe70d0", 1), client.ErrWriteUncertain},
		{"invalid version", 200, strings.Replace(valid, `"version":2`, `"version":0`, 1), client.ErrWriteUncertain},
		{"unchanged version", 200, strings.Replace(valid, `"version":2`, `"version":1`, 1), client.ErrWriteUncertain},
		{"invalid status", 200, strings.Replace(valid, "available", marker, 1), client.ErrWriteUncertain},
		{"invalid expiry", 200, strings.Replace(valid, `"status":"available"`, `"status":"available","expires_at":"0001-01-01T00:00:00Z"`, 1), client.ErrWriteUncertain},
		{"oversized metadata", 200, valid + strings.Repeat(" ", 64<<10), client.ErrWriteUncertain},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			_, err := adminsecrets.Save(t.Context(), newAPIClient(t, server.URL), key, marker, &expected)
			if !errors.Is(err, test.want) || requests.Load() != 1 || strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("unsafe acknowledgement handling: error=%v requests=%d", err, requests.Load())
			}
		})
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	api := newAPIClient(t, server.URL)
	for _, metadata := range []adminsecrets.Metadata{
		{ID: secretTestID, AgentType: "other-agent", Name: key.Name, Version: 1, Status: "available"},
		{ID: secretTestID, AgentType: key.Scope.AgentType, Name: key.Name, Version: ^uint64(0), Status: "available"},
	} {
		if _, err := adminsecrets.Save(t.Context(), api, key, marker, &metadata); err == nil {
			t.Error("invalid concurrency metadata was accepted")
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid concurrency metadata reached the API")
	}
}

func secretCommand(t *testing.T, connection client.Connection, input io.Reader, args ...string) ([]byte, error) {
	t.Helper()
	var output, diagnostics bytes.Buffer
	err := cli.RunWithOptions(t.Context(), args, input, &output, &diagnostics, cli.Options{Connection: &connection})
	text := output.String() + diagnostics.String()
	if err != nil {
		text += err.Error()
	}
	for _, marker := range []string{"secret-value-must-not-be-printed", "renewed-secret-value", "server-private-value", "private-positional-value", "private-flag-value", connection.Credentials.Password} {
		if marker != "" && strings.Contains(text, marker) {
			t.Fatal("secret command disclosed a private value")
		}
	}
	return output.Bytes(), err
}

type secretReader struct{ reads *atomic.Int32 }

func (r secretReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, io.EOF
}

type secretAPI struct {
	server       *httptest.Server
	mu           sync.Mutex
	saved        *adminsecrets.Metadata
	requests     []string
	values       []string
	metadataRead chan struct{}
}

func newSecretAPI(t *testing.T) *secretAPI {
	t.Helper()
	fixture := &secretAPI{metadataRead: make(chan struct{}, 1)}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets" {
			fixture.requests = append(fixture.requests, "GET list")
			if r.URL.Query().Get("agent_type") != "builder-agent" {
				t.Error("list lost agent scope")
			}
			item, _ := json.Marshal(fixture.saved)
			// Extra fields from a server must not leak through metadata output.
			item = bytes.Replace(item, []byte(`"status":`), []byte(`"value":"server-private-value","status":`), 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []json.RawMessage{item}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets/requests" {
			fixture.requests = append(fixture.requests, "GET requests")
			if r.URL.Query().Get("agent_type") != "builder-agent" {
				t.Error("requests lost agent scope")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"id": secretTestID, "agent_type": "builder-agent", "name": "GITHUB_TOKEN", "agent_id": "builder-agent-deadbeef",
				"requested_at": time.Now().UTC(), "expires_at": time.Now().Add(time.Hour).UTC(), "status": "needs_value", "value": "server-private-value",
			}}})
			return
		}
		if r.URL.Path != "/api/v1/secrets/builder-agent/GITHUB_TOKEN" && !(r.Method == http.MethodPost && r.URL.Path == "/api/v1/secrets") {
			t.Errorf("unexpected secret path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet {
			fixture.requests = append(fixture.requests, "GET secret")
			select {
			case fixture.metadataRead <- struct{}{}:
			default:
			}
			if fixture.saved == nil {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":"secret_not_found"}`)
				return
			}
		} else if r.Method == http.MethodDelete {
			fixture.requests = append(fixture.requests, "DELETE")
			if fixture.saved == nil || r.URL.Query().Get("id") != fixture.saved.ID || r.URL.Query().Get("version") != "3" {
				t.Error("delete lost the observed identity or version")
			}
			fixture.saved = nil
			w.WriteHeader(http.StatusNoContent)
			return
		} else {
			fixture.requests = append(fixture.requests, r.Method)
			var input struct {
				AgentType string     `json:"agent_type"`
				Name      string     `json:"name"`
				Value     *string    `json:"value"`
				ID        string     `json:"id"`
				Version   uint64     `json:"version"`
				ExpiresAt *time.Time `json:"expires_at"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Value == nil {
				t.Error("invalid secret mutation body")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.values = append(fixture.values, *input.Value)
			if r.Method == http.MethodPost {
				if fixture.saved != nil || input.AgentType != "builder-agent" || input.Name != "GITHUB_TOKEN" || input.ID != "" || input.Version != 0 {
					t.Error("create did not preserve scope or new-secret semantics")
				}
				fixture.saved = &adminsecrets.Metadata{ID: secretTestID, AgentType: "builder-agent", Name: "GITHUB_TOKEN", Version: 1, Status: "available"}
				w.WriteHeader(http.StatusCreated)
			} else if r.Method == http.MethodPut {
				if fixture.saved == nil || input.ID != fixture.saved.ID || input.Version != fixture.saved.Version {
					t.Error("replace lost the observed identity or version")
				}
				if fixture.saved.ExpiresAt != nil && fixture.saved.ExpiresAt.After(time.Now()) && (input.ExpiresAt == nil || !input.ExpiresAt.Equal(*fixture.saved.ExpiresAt)) {
					t.Error("replace lost the future expiry")
				}
				fixture.saved.Version++
				fixture.saved.Status = "available"
				fixture.saved.ExpiresAt = input.ExpiresAt
			} else {
				t.Error("unexpected secret method")
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(fixture.saved)
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (f *secretAPI) connection() client.Connection {
	return client.Connection{URL: f.server.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}}
}
