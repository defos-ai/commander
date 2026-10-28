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
	"sync/atomic"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/cli"
	"github.com/defos-ai/commander/apps/admin/client"
	adminsecrets "github.com/defos-ai/commander/apps/admin/secrets"
	"github.com/defos-ai/commander/apps/admin/tests/fakes"
	"github.com/defos-ai/commander/apps/admin/tests/fixtures"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/host"
)

func TestSecretCommandsPreserveValuesAndVersionedMutations(t *testing.T) {
	fixture := fixtures.NewSecretAPI(t)
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
				case <-fixture.MetadataRead():
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
		output, err := secretCommand(t, fixture.Connection(), input, "secrets", "set", "builder-agent", "GITHUB_TOKEN", "--json")
		if err != nil {
			t.Fatalf("set secret: %v", err)
		}
		if written != nil {
			if err := <-written; err != nil {
				t.Fatalf("write piped secret: %v", err)
			}
		}
		var metadata adminsecrets.Metadata
		if json.Unmarshal(output, &metadata) != nil || metadata.ID != fakes.SecretID || metadata.Version == 0 || metadata.Status != "available" || strings.Contains(string(output), value) || strings.Contains(string(output), "renewed-secret-value") {
			t.Fatal("set did not return valid metadata only")
		}
		state := fixture.Snapshot()
		if state.Values[len(state.Values)-1] != current {
			t.Error("stdin secret bytes changed")
		}
		version := state.Saved.Version
		if version < 3 {
			expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			if version == 2 {
				expiry = time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			}
			fixture.SetExpiry(expiry)
		} else if state.Saved.ExpiresAt != nil {
			t.Error("renewing an expired secret retained its expired timestamp")
		}
	}
	for _, args := range [][]string{
		{"secrets", "list", "builder-agent", "--json"},
		{"secrets", "requests", "builder-agent", "--json"},
	} {
		output, err := secretCommand(t, fixture.Connection(), nil, args...)
		var result struct {
			Items []json.RawMessage `json:"items"`
		}
		if err != nil || json.Unmarshal(output, &result) != nil || len(result.Items) != 1 || bytes.Contains(output, []byte(`"value"`)) || bytes.Contains(output, []byte("server-private-value")) {
			t.Fatalf("list must return metadata only: %v", err)
		}
	}
	if _, err := secretCommand(t, fixture.Connection(), nil, "secrets", "delete", "builder-agent", "GITHUB_TOKEN", "--json"); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	state := fixture.Snapshot()
	if state.Saved != nil || !reflect.DeepEqual(state.Requests, []string{"GET secret", "POST", "GET secret", "PUT", "GET secret", "PUT", "GET list", "GET requests", "GET secret", "DELETE"}) {
		t.Fatalf("mutations skipped version reads or were retried: %v", state.Requests)
	}
}

func TestSecretCommandsRejectUnsafeInputBeforeMutation(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"route_not_found"}`)
	}))
	defer server.Close()
	connection := client.Connection{URL: server.URL, Credentials: host.BasicCredentials{Username: "operator", Password: "private-password"}}
	reader := &fakes.CountingReader{}
	_, err := secretCommand(t, connection, reader, "secrets", "set", "builder-agent", "GITHUB_TOKEN")
	if err == nil || reader.ReadCount() != 0 || writes.Load() != 0 {
		t.Fatal("an unrelated 404 prompted for a value or created a secret")
	}
	fixture := fixtures.NewSecretAPI(t)
	for _, args := range [][]string{
		{"secrets", "set", "builder-agent", "GITHUB_TOKEN", "private-positional-value"},
		{"secrets", "set", "builder-agent", "GITHUB_TOKEN", "--value", "private-flag-value"},
	} {
		output, err := secretCommand(t, fixture.Connection(), reader, args...)
		var usage *cli.UsageError
		if !errors.As(err, &usage) || reader.ReadCount() != 0 || strings.Contains(err.Error()+string(output), "private-") {
			t.Fatal("a secret argument was accepted, read stdin or exposed a value")
		}
	}
	for _, value := range []string{strings.Repeat("x", 64<<10+1), string([]byte{0xff})} {
		if _, err := secretCommand(t, fixture.Connection(), strings.NewReader(value), "secrets", "set", "builder-agent", "GITHUB_TOKEN"); err == nil {
			t.Error("unsafe or oversized stdin secret was accepted")
		}
	}
	if len(fixture.Snapshot().Values) != 0 {
		t.Fatal("invalid command input submitted a secret")
	}
}

func TestSecretWritesValidateMetadataWithoutRetrying(t *testing.T) {
	const marker = "private-secret-never-display"
	valid := `{"id":"` + fakes.SecretID + `","agent_type":"builder-agent","name":"GITHUB_TOKEN","version":2,"status":"available"}`
	key := secretmodel.Key{Scope: secretmodel.Scope{AgentType: "builder-agent"}, Name: "GITHUB_TOKEN"}
	expected := adminsecrets.Metadata{ID: fakes.SecretID, AgentType: key.Scope.AgentType, Name: key.Name, Version: 1, Status: "available"}
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
		{"invalid identity", 200, strings.Replace(valid, fakes.SecretID, marker, 1), client.ErrWriteUncertain},
		{"changed identity", 200, strings.Replace(valid, fakes.SecretID, "5123e0e2-d5e1-41d0-a321-5cce05fe70d0", 1), client.ErrWriteUncertain},
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
			_, err := adminsecrets.Save(t.Context(), fixtures.NewClient(t, server.URL), key, marker, &expected)
			if !errors.Is(err, test.want) || requests.Load() != 1 || strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("unsafe acknowledgement handling: error=%v requests=%d", err, requests.Load())
			}
		})
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	api := fixtures.NewClient(t, server.URL)
	for _, metadata := range []adminsecrets.Metadata{
		{ID: fakes.SecretID, AgentType: "other-agent", Name: key.Name, Version: 1, Status: "available"},
		{ID: fakes.SecretID, AgentType: key.Scope.AgentType, Name: key.Name, Version: ^uint64(0), Status: "available"},
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
