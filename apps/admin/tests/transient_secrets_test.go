package tests

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/defos-ai/commander/apps/admin/client"
	"github.com/defos-ai/commander/apps/admin/secrets"
	"github.com/defos-ai/commander/apps/admin/tests/fixtures"
)

func TestTransientSubmissionUncertaintyRequiresMetadataReadback(t *testing.T) {
	request := fixtures.TransientSecretRequest()
	const value = "private-verification-code"
	var submissions atomic.Int32
	api := fixtures.SecretAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/secret-requests/"+request.ID+"/values":
			submissions.Add(1)
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["value"] != value {
				t.Error("submission did not contain exactly the requested value")
			}
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "secret_delivery_uncertain", "message": value})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/secret-requests/"+request.ID:
			metadata := request
			metadata.Status = "fulfilled"
			_ = json.NewEncoder(w).Encode(metadata)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	err := secrets.ProvideTransient(context.Background(), api, request.ID, value)

	if !errors.Is(err, client.ErrWriteUncertain) {
		t.Fatalf("submission should be uncertain: %v", err)
	}
	if strings.Contains(err.Error(), value) {
		t.Fatal("remote error exposed the one-time value")
	}

	observed, err := secrets.GetTransientRequest(context.Background(), api, request.ID)

	if err != nil || observed == nil || observed.Status != "fulfilled" || observed.CommandID != request.CommandID {
		t.Fatalf("receipt readback did not retain the exact execution: %v, %v", observed, err)
	}
	if submissions.Load() != 1 {
		t.Fatal("uncertain submission was retried")
	}
}

func TestTransientMetadataDoesNotCrossRequestOrRecipient(t *testing.T) {
	request := fixtures.TransientSecretRequest()
	api := fixtures.SecretAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/secret-requests" {
			if r.URL.Query().Get("type") != "transient" || r.URL.Query().Get("agent_id") != "browser-agent-00000002" {
				t.Error("request discovery lost its type or recipient filter")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []secrets.TransientRequest{request}})
			return
		}
		if r.URL.Path != "/api/v1/secret-requests/"+request.ID {
			t.Errorf("unexpected metadata path %s", r.URL.Path)
		}
		metadata := request
		metadata.ID = "00000000-0000-4000-8000-000000000005"
		_ = json.NewEncoder(w).Encode(metadata)
	}))

	items, err := secrets.TransientRequests(context.Background(), api, "browser-agent-00000002")

	if items != nil || !errors.Is(err, client.ErrInvalidResponse) {
		t.Fatalf("accepted metadata for another recipient: %v, %v", items, err)
	}

	observed, err := secrets.GetTransientRequest(context.Background(), api, request.ID)

	if observed != nil || !errors.Is(err, client.ErrInvalidResponse) {
		t.Fatalf("accepted another request's metadata: %v, %v", observed, err)
	}
}
