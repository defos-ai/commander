package fakes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	adminsecrets "github.com/defos-ai/commander/apps/admin/secrets"
)

const SecretID = "da378c90-63eb-46da-b186-552e1e144510"

type SecretSnapshot struct {
	Saved    *adminsecrets.Metadata
	Requests []string
	Values   []string
}

type SecretAPI struct {
	t            testing.TB
	saved        *adminsecrets.Metadata
	requests     []string
	values       []string
	metadataRead chan struct{}
}

func NewSecretAPI(t testing.TB) *SecretAPI {
	t.Helper()
	return &SecretAPI{t: t, metadataRead: make(chan struct{}, 1)}
}

func (f *SecretAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets" {
		f.requests = append(f.requests, "GET list")
		if r.URL.Query().Get("agent_type") != "builder-agent" {
			f.t.Error("list lost agent scope")
		}
		item, _ := json.Marshal(f.saved)
		// Extra fields from a server must not leak through metadata output.
		item = bytes.Replace(item, []byte(`"status":`), []byte(`"value":"server-private-value","status":`), 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []json.RawMessage{item}})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/secrets/requests" {
		f.requests = append(f.requests, "GET requests")
		if r.URL.Query().Get("agent_type") != "builder-agent" {
			f.t.Error("requests lost agent scope")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
			"id": SecretID, "agent_type": "builder-agent", "name": "GITHUB_TOKEN", "agent_id": "builder-agent-deadbeef",
			"requested_at": time.Now().UTC(), "expires_at": time.Now().Add(time.Hour).UTC(), "status": "needs_value", "value": "server-private-value",
		}}})
		return
	}
	if r.URL.Path != "/api/v1/secrets/builder-agent/GITHUB_TOKEN" && !(r.Method == http.MethodPost && r.URL.Path == "/api/v1/secrets") {
		f.t.Errorf("unexpected secret path: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		f.requests = append(f.requests, "GET secret")
		select {
		case f.metadataRead <- struct{}{}:
		default:
		}
		if f.saved == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"secret_not_found"}`)
			return
		}
	} else if r.Method == http.MethodDelete {
		f.requests = append(f.requests, "DELETE")
		if f.saved == nil || r.URL.Query().Get("id") != f.saved.ID || r.URL.Query().Get("version") != "3" {
			f.t.Error("delete lost the observed identity or version")
		}
		f.saved = nil
		w.WriteHeader(http.StatusNoContent)
		return
	} else {
		f.requests = append(f.requests, r.Method)
		var input struct {
			AgentType string     `json:"agent_type"`
			Name      string     `json:"name"`
			Value     *string    `json:"value"`
			ID        string     `json:"id"`
			Version   uint64     `json:"version"`
			ExpiresAt *time.Time `json:"expires_at"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Value == nil {
			f.t.Error("invalid secret mutation body")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.values = append(f.values, *input.Value)
		if r.Method == http.MethodPost {
			if f.saved != nil || input.AgentType != "builder-agent" || input.Name != "GITHUB_TOKEN" || input.ID != "" || input.Version != 0 {
				f.t.Error("create did not preserve scope or new-secret semantics")
			}
			f.saved = &adminsecrets.Metadata{ID: SecretID, AgentType: "builder-agent", Name: "GITHUB_TOKEN", Version: 1, Status: "available"}
			w.WriteHeader(http.StatusCreated)
		} else if r.Method == http.MethodPut {
			if f.saved == nil || input.ID != f.saved.ID || input.Version != f.saved.Version {
				f.t.Error("replace lost the observed identity or version")
			}
			if f.saved.ExpiresAt != nil && f.saved.ExpiresAt.After(time.Now()) && (input.ExpiresAt == nil || !input.ExpiresAt.Equal(*f.saved.ExpiresAt)) {
				f.t.Error("replace lost the future expiry")
			}
			f.saved.Version++
			f.saved.Status = "available"
			f.saved.ExpiresAt = input.ExpiresAt
		} else {
			f.t.Error("unexpected secret method")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	}
	_ = json.NewEncoder(w).Encode(f.saved)
}

func (f *SecretAPI) MetadataRead() <-chan struct{} { return f.metadataRead }

func (f *SecretAPI) Snapshot() SecretSnapshot {
	state := SecretSnapshot{Requests: slices.Clone(f.requests), Values: slices.Clone(f.values)}
	if f.saved != nil {
		saved := *f.saved
		if saved.ExpiresAt != nil {
			expiry := *saved.ExpiresAt
			saved.ExpiresAt = &expiry
		}
		state.Saved = &saved
	}
	return state
}

func (f *SecretAPI) SetExpiry(expiry time.Time) {
	f.saved.ExpiresAt = &expiry
	if !expiry.After(time.Now()) {
		f.saved.Status = "expired"
	}
}
