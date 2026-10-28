package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/adapters/api/endpoints"
	"github.com/defos-ai/commander/framework/crypto"
	secretmodel "github.com/defos-ai/commander/framework/domain/secrets"
	"github.com/defos-ai/commander/framework/id"
	secretservice "github.com/defos-ai/commander/framework/secrets"
)

func TestPendingSecretRequestFailuresAreSanitized(t *testing.T) {
	for _, stage := range []string{"requests", "metadata"} {
		for _, tc := range []struct {
			code   string
			status int
			err    error
		}{
			{"internal_error", http.StatusInternalServerError, errors.New(secretTestValue + " private backend failure")},
			{"request_timeout", http.StatusGatewayTimeout, context.DeadlineExceeded},
			{"request_canceled", http.StatusServiceUnavailable, context.Canceled},
		} {
			t.Run(stage+"/"+tc.code, func(t *testing.T) {
				calls := 0
				fail := func(ctx context.Context) error {
					calls++
					deadline, ok := ctx.Deadline()
					if remaining := time.Until(deadline); !ok || remaining <= 0 || remaining > 10*time.Second {
						t.Fatal("secret request lookup lost its bounded context")
					}
					return tc.err
				}
				requests := pendingRequestReader{list: func(ctx context.Context) ([]secretmodel.RequestRecord, error) {
					if stage == "requests" {
						return []secretmodel.RequestRecord{pendingRequestRecord("TOKEN")}, fail(ctx)
					}
					return []secretmodel.RequestRecord{pendingRequestRecord("FIRST_TOKEN"), pendingRequestRecord("SECOND_TOKEN")}, nil
				}}
				metadataCalls := 0
				store := requestedMetadataReader{get: func(ctx context.Context, _ secretmodel.Key) (secretmodel.Metadata, error) {
					metadataCalls++
					if metadataCalls == 1 {
						return secretmodel.Metadata{}, secretmodel.ErrUnknownSecret
					}
					return secretmodel.Metadata{}, fail(ctx)
				}}
				result := pendingRequestResponse(t.Context(), pendingRequestService(t, store), requests)
				assertSecretError(t, result, tc.status, tc.code)
				if calls != 1 {
					t.Fatal("failed lookup was retried or skipped")
				}
				if result.header.Get("Cache-Control") != "no-store" {
					t.Fatal("error response may be cached")
				}
			})
		}
	}
}

func TestPendingSecretRequestLookupPreservesCallerCancellation(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name, expected := "canceled", "request_canceled"
		status := http.StatusServiceUnavailable
		if expired {
			name, expected, status = "deadline", "request_timeout", http.StatusGatewayTimeout
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			} else {
				cancel()
			}
			defer cancel()
			requests := pendingRequestReader{list: func(ctx context.Context) ([]secretmodel.RequestRecord, error) {
				if ctx.Err() == nil {
					t.Fatal("caller cancellation was lost")
				}
				return nil, ctx.Err()
			}}
			assertSecretError(t, pendingRequestResponse(ctx, pendingRequestService(t, requestedMetadataReader{}), requests), status, expected)
		})
	}
}

func TestPendingSecretRequestsRejectInvalidSavedEvidence(t *testing.T) {
	for _, corrupt := range []string{"request", "delivery", "metadata", "metadata_key"} {
		t.Run(corrupt, func(t *testing.T) {
			valid, invalid := pendingRequestRecord("FIRST_TOKEN"), pendingRequestRecord("SECOND_TOKEN")
			if corrupt == "request" {
				invalid.Request.ID = secretTestValue
			}
			if corrupt == "delivery" {
				invalid.Delivery = &secretmodel.Delivery{ID: secretTestValue}
			}
			requests := pendingRequestReader{list: func(context.Context) ([]secretmodel.RequestRecord, error) {
				return []secretmodel.RequestRecord{valid, invalid}, nil
			}}
			store := requestedMetadataReader{get: func(_ context.Context, key secretmodel.Key) (secretmodel.Metadata, error) {
				if key.Name == "FIRST_TOKEN" {
					return secretmodel.Metadata{}, secretmodel.ErrUnknownSecret
				}
				metadata := secretmodel.Metadata{ID: id.New(), Key: key, Version: 1, CreatedAt: invalid.Request.RequestedAt, UpdatedAt: invalid.Request.RequestedAt}
				if corrupt == "metadata_key" {
					metadata.Key.Name = "OTHER_TOKEN"
				} else {
					metadata.Version = 0
				}
				return metadata, nil
			}}
			assertSecretError(t, pendingRequestResponse(t.Context(), pendingRequestService(t, store), requests), http.StatusInternalServerError, "internal_error")
		})
	}
}

func TestPendingSecretRequestsRequireBothDependencies(t *testing.T) {
	service := pendingRequestService(t, requestedMetadataReader{})
	for _, tc := range []struct {
		name     string
		service  *secretservice.Service
		requests secretmodel.RequestStore
	}{
		{name: "both absent"},
		{name: "service absent", requests: pendingRequestReader{}},
		{name: "requests absent", service: service},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertSecretError(t, pendingRequestResponse(t.Context(), tc.service, tc.requests), http.StatusServiceUnavailable, "secrets_unavailable")
		})
	}
}

func TestPendingSecretRequestOpenAPIDescribesSafeProgress(t *testing.T) {
	fixture := newFixture(t)
	server := startAPI(t, append(fixture.localArgs(), "--swagger=true"))
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
				Schema   struct {
					Pattern   string `json:"pattern"`
					MaxLength int    `json:"maxLength"`
				} `json:"schema"`
			} `json:"parameters"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	result := server.request(t, http.MethodGet, "/openapi.json", authenticate)
	if result.status != http.StatusOK || json.Unmarshal([]byte(result.body), &document) != nil {
		t.Fatal("could not read pending secret request documentation")
	}
	operation, exists := document.Paths["/api/v1/secrets/requests"]["get"]
	if !exists {
		t.Fatal("pending secret request query is undocumented")
	}
	filters := make([]string, 0, len(operation.Parameters))
	for _, parameter := range operation.Parameters {
		filters = append(filters, parameter.Name)
		if parameter.Required || parameter.In != "query" || parameter.Schema.Pattern == "" || parameter.Schema.MaxLength != 63 {
			t.Errorf("filter %s must be optional and constrain its identifier", parameter.Name)
		}
	}
	slices.Sort(filters)
	if !slices.Equal(filters, []string{"agent_id", "agent_type"}) {
		t.Fatal("pending secret request filters differ from the public contract")
	}
	for _, status := range []string{"200", "400", "401", "403", "500", "503", "504"} {
		if len(operation.Responses[status]) == 0 {
			t.Errorf("missing pending request response %s", status)
		}
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(document.Components.Schemas["SecretRequest"], &schema) != nil {
		t.Fatal("could not read pending request response schema")
	}
	properties := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		properties = append(properties, name)
	}
	slices.Sort(properties)
	if !slices.Equal(properties, []string{"agent_id", "agent_type", "expires_at", "id", "name", "requested_at", "status"}) {
		t.Fatal("pending request schema must expose only explicit safe metadata")
	}
	slices.Sort(schema.Required)
	if !slices.Equal(schema.Required, properties) {
		t.Fatal("pending request schema must require all metadata fields")
	}
	if !slices.Equal(schema.Properties["status"].Enum, []string{"needs_value", "awaiting_delivery", "awaiting_installation"}) {
		t.Fatal("pending request progress states are undocumented")
	}
}

type pendingRequestReader struct {
	secretmodel.RequestStore
	list func(context.Context) ([]secretmodel.RequestRecord, error)
}

func (store pendingRequestReader) ListPending(ctx context.Context) ([]secretmodel.RequestRecord, error) {
	return store.list(ctx)
}

type requestedMetadataReader struct {
	secretmodel.SecretStore
	get func(context.Context, secretmodel.Key) (secretmodel.Metadata, error)
}

func (store requestedMetadataReader) GetMetadata(ctx context.Context, key secretmodel.Key) (secretmodel.Metadata, error) {
	return store.get(ctx, key)
}

func pendingRequestRecord(name string) secretmodel.RequestRecord {
	now := time.Now().UTC()
	return secretmodel.RequestRecord{Request: secretmodel.Request{
		ID: id.New(), AgentID: "builder-agent-01234567", Key: secretmodel.Key{Scope: secretmodel.Scope{AgentType: "builder-agent"}, Name: name},
		Status: secretmodel.Pending, RequestedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
	}}
}

func pendingRequestService(t *testing.T, store secretmodel.SecretStore) *secretservice.Service {
	t.Helper()
	cipher, err := crypto.NewSecretCipher(secretTestKey)
	if err != nil {
		t.Fatal(err)
	}
	service, err := secretservice.NewService(store, cipher)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func pendingRequestResponse(ctx context.Context, service *secretservice.Service, requests secretmodel.RequestStore) response {
	mux := http.NewServeMux()
	endpoints.RegisterSecretRequests(mux, service, requests)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/secrets/requests", nil))
	return response{status: recorder.Code, header: recorder.Header(), body: recorder.Body.String()}
}
