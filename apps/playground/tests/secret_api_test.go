package playground_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	secretapi "github.com/defos-ai/commander/apps/playground/internal/api"
	"github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/host"
)

func TestSecretAPIAuthenticatedCreateAndVersionedReplace(t *testing.T) {
	key := secrets.Key{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: "GITHUB_TOKEN"}
	const secretID = "da378c90-63eb-46da-b186-552e1e144510"
	var saved *secretapi.Metadata
	var mutations int
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "commander" || password != "api-password" || r.Header.Get("Cookie") != "" {
			t.Error("missing API authentication or unexpected cookies")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		path := "/prefix/api/v1/secrets/builder-agent/GITHUB_TOKEN"
		if r.Method == http.MethodPost {
			path = "/prefix/api/v1/secrets"
		}
		if r.URL.Path != path {
			t.Error("API base prefix or scoped path changed")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodGet {
			if saved == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		} else {
			mutations++
			var input struct {
				AgentType string     `json:"agent_type"`
				Name      string     `json:"name"`
				Value     *string    `json:"value"`
				ID        string     `json:"id"`
				Version   uint64     `json:"version"`
				ExpiresAt *time.Time `json:"expires_at"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Value == nil || r.Header.Get("Content-Type") != "application/json" {
				t.Error("invalid secret submission")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.Method == http.MethodPost {
				if input.AgentType != key.Scope.AgentType || input.Name != key.Name || *input.Value != "" {
					t.Error("create lost scope, name or valid empty secret value")
				}
				saved = &secretapi.Metadata{ID: secretID, AgentType: key.Scope.AgentType, Name: key.Name, Version: 1, Status: "available", ExpiresAt: &expiresAt}
				w.WriteHeader(http.StatusCreated)
			} else if r.Method == http.MethodPut {
				if input.ID != saved.ID || input.Version != saved.Version || input.ExpiresAt == nil || !input.ExpiresAt.Equal(expiresAt) || *input.Value != "replacement-secret" {
					t.Error("replace did not preserve concurrency or expiry")
				}
				saved.Version++
			} else {
				t.Error("unexpected mutation method")
			}
		}
		_ = json.NewEncoder(w).Encode(saved)
	}))
	defer endpoint.Close()
	client, err := secretapi.New(endpoint.URL+"/prefix/", host.BasicCredentials{Username: "commander", Password: "api-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	metadata, err := client.Metadata(t.Context(), key)
	if err != nil || metadata != nil {
		t.Fatalf("missing secret lookup: %v", err)
	}
	if err := client.Save(t.Context(), key, "", nil); err != nil {
		t.Fatal(err)
	}
	metadata, err = client.Metadata(t.Context(), key)
	if err != nil || metadata == nil || metadata.Version != 1 {
		t.Fatalf("created secret lookup: %v", err)
	}
	if err := client.Save(t.Context(), key, "replacement-secret", metadata); err != nil {
		t.Fatal(err)
	}
	if mutations != 2 {
		t.Fatal("secret mutation was retried")
	}
}

func TestSecretAPIFailuresDoNotLeakOrRetry(t *testing.T) {
	key := secrets.Key{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: "GITHUB_TOKEN"}
	const marker = "private-value-must-never-be-displayed"
	validMetadata := `{"id":"da378c90-63eb-46da-b186-552e1e144510","agent_type":"builder-agent","name":"GITHUB_TOKEN","version":1,"status":"available"}`
	for _, test := range []struct {
		name   string
		status int
		body   string
		is     error
	}{
		{"authentication", 401, marker, secretapi.ErrAuthentication},
		{"conflict", 409, marker, secretapi.ErrConflict},
		{"uncertain write", 503, marker, secretapi.ErrWriteUncertain},
		{"invalid metadata", 201, marker, secretapi.ErrWriteUncertain},
		{"wrong scope", 201, strings.Replace(validMetadata, "builder-agent", "another-agent", 1), secretapi.ErrWriteUncertain},
		{"invalid identity", 201, strings.Replace(validMetadata, "da378c90-63eb-46da-b186-552e1e144510", marker, 1), secretapi.ErrWriteUncertain},
		{"invalid version", 201, strings.Replace(validMetadata, `"version":1`, `"version":0`, 1), secretapi.ErrWriteUncertain},
		{"invalid status", 201, strings.Replace(validMetadata, "available", marker, 1), secretapi.ErrWriteUncertain},
		{"oversize metadata", 201, validMetadata + strings.Repeat(" ", 64<<10), secretapi.ErrWriteUncertain},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer endpoint.Close()
			client, err := secretapi.New(endpoint.URL, host.BasicCredentials{Username: "commander", Password: marker})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = client.Save(t.Context(), key, marker, nil)
			if !errors.Is(err, test.is) || strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), endpoint.URL) {
				t.Fatal("secret failure lost its type or exposed private data")
			}
			if requests.Load() != 1 {
				t.Fatal("failed secret save was retried")
			}
			if strings.Contains(fmt.Sprintf("%v %+v %#v", client, client, client), marker) {
				t.Fatal("client formatting exposed credentials")
			}
		})
	}
}

func TestSecretAPIDoesNotFollowRedirectsAndHonorsCancellation(t *testing.T) {
	key := secrets.Key{Scope: secrets.Scope{AgentType: "builder-agent"}, Name: "GITHUB_TOKEN"}
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer endpoint.Close()
	client, err := secretapi.New(endpoint.URL, host.BasicCredentials{Username: "commander", Password: "private-api-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Metadata(t.Context(), key); err == nil {
		t.Fatal("redirected lookup succeeded")
	}
	if err := client.Save(t.Context(), key, "private-secret-value", nil); err == nil {
		t.Fatal("redirected save succeeded")
	}
	if redirected.Load() != 0 || requests.Load() != 2 {
		t.Fatal("redirect forwarded authentication or secret bytes")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Metadata(ctx, key); !errors.Is(err, context.Canceled) {
		t.Fatal("lookup did not preserve context cancellation")
	}
	if err := client.Save(ctx, key, "private-secret-value", nil); !errors.Is(err, context.Canceled) {
		t.Fatal("save did not preserve context cancellation")
	}
	if requests.Load() != 2 {
		t.Fatal("canceled request reached the API")
	}
	for _, raw := range []string{"", "localhost:4274", "ftp://api.example", "http://user:private@example.org", "https://api.example?token=private", "https://api.example?", "https://api.example#private", "https://api.example#", "https:///path"} {
		if secretapi.ValidateURL(raw) == nil {
			t.Fatal("invalid API URL was accepted")
		}
	}
}
