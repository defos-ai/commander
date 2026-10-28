package packages_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/packages"
	fakes "github.com/defos-ai/commander/framework/tests/fakes/agent"
	fixtures "github.com/defos-ai/commander/framework/tests/fixtures/agent"
)

func checkPublicMetadataRequest(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Method != http.MethodGet || request.URL.Scheme != "https" {
		t.Fatalf("metadata request must be an HTTPS GET: %s %s", request.Method, request.URL)
	}
	for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
		if request.Header.Get(header) != "" {
			t.Errorf("public metadata request includes %s", header)
		}
	}
	deadline, ok := request.Context().Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
		t.Fatal("metadata request needs a positive deadline of at most 30 seconds")
	}
}

func TestClientLoadsConfiguredSources(t *testing.T) {
	const source = "https://sources.example/agents.yaml"
	client := packages.NewClient(fakes.MetadataTransport(func(request *http.Request) (*http.Response, error) {
		checkPublicMetadataRequest(t, request)
		if request.URL.String() != source {
			t.Fatalf("unexpected sources URL: %s", request.URL)
		}
		return fakes.MetadataResponse(request, http.StatusOK, io.NopCloser(strings.NewReader(fixtures.ClientSources))), nil
	}))
	t.Cleanup(client.CloseIdleConnections)

	sources, err := client.LoadSources(context.Background(), source)

	if err != nil {
		t.Fatal(err)
	}
	entry, err := sources.Lookup("builder")
	if err != nil || entry.Description != "Build an agent" || entry.Source != "github.com/acme/agents/apps/agents/builder@stable" {
		t.Fatalf("sources entry: %#v, %v", entry, err)
	}
}

func TestClientRejectsUnsafeSourcesBeforeRequest(t *testing.T) {
	client := packages.NewClient(fakes.MetadataTransport(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("invalid input contacted the network: %s", request.URL)
		return nil, nil
	}))
	t.Cleanup(client.CloseIdleConnections)
	for _, source := range []string{
		"http://sources.example/private-marker",
		"https://user:private-marker@sources.example/agents.yaml",
		"https://sources.example/agents.yaml?token=private-marker",
		"https://sources.example/agents.yaml#private-marker",
		"https:private-marker",
		"/private-marker",
	} {
		if _, err := client.LoadSources(context.Background(), source); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Errorf("unsafe sources URL should fail without echoing input: %v", err)
		}
	}
}

func TestClientBoundsMetadataAndClosesBodies(t *testing.T) {
	for _, metadata := range []struct {
		name, body string
		limit      int
		load       func(*packages.Client) error
	}{
		{"sources", fixtures.ClientSources, packages.MaxSourcesBytes, func(client *packages.Client) error {
			_, err := client.LoadSources(context.Background(), "https://sources.example/agents.yaml")
			return err
		}},
	} {
		for _, oversized := range []bool{false, true} {
			name, size := metadata.name+" at limit", metadata.limit
			if oversized {
				name, size = metadata.name+" over limit", metadata.limit*2
			}
			t.Run(name, func(t *testing.T) {
				body := fakes.NewMetadataBody(strings.NewReader(metadata.body + strings.Repeat(" ", size-len(metadata.body))))
				client := packages.NewClient(fakes.MetadataTransport(func(request *http.Request) (*http.Response, error) {
					return fakes.MetadataResponse(request, http.StatusOK, body), nil
				}))
				t.Cleanup(client.CloseIdleConnections)
				err := metadata.load(client)
				if (err != nil) != oversized {
					t.Fatalf("size %d, limit %d: %v", size, metadata.limit, err)
				}
				if body.BytesRead() > metadata.limit+1 || !body.Closed() {
					t.Fatalf("metadata body read %d bytes, limit %d; closed: %t", body.BytesRead(), metadata.limit, body.Closed())
				}
			})
		}
	}
}

func TestClientRejectsHTTPFailuresWithoutLeakingMetadata(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		location string
		want     string
	}{
		{"forbidden", http.StatusForbidden, "", "rate"},
		{"rate limited", http.StatusTooManyRequests, "", "rate"},
		{"missing", http.StatusNotFound, "", "not found"},
		{"server error", http.StatusInternalServerError, "", ""},
		{"same host redirect", http.StatusFound, "https://sources.example/private-marker", ""},
		{"cross host redirect", http.StatusTemporaryRedirect, "https://private-marker.example/sources", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			body := fakes.NewMetadataBody(strings.NewReader("private-marker response body"))
			client := packages.NewClient(fakes.MetadataTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 {
					t.Fatal("client followed a metadata redirect")
				}
				response := fakes.MetadataResponse(request, tc.status, body)
				response.Header.Set("Location", tc.location)
				return response, nil
			}))
			t.Cleanup(client.CloseIdleConnections)
			_, err := client.LoadSources(context.Background(), "https://sources.example/private-marker.yaml")
			if err == nil || strings.Contains(err.Error(), "private-marker") || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("unexpected HTTP failure: %v", err)
			}
			if !body.Closed() {
				t.Fatal("failed response body was not closed")
			}
		})
	}
}

func TestClientSanitizesTransportAndParseErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body io.Reader
		err  error
	}{
		{"transport", nil, errors.New("private-marker transport failure")},
		{"body read", fakes.MetadataReadError{}, nil},
		{"sources", strings.NewReader("schema_version: 1\nprivate-marker: true\nagents: {}\n"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := packages.NewClient(fakes.MetadataTransport(func(request *http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return fakes.MetadataResponse(request, http.StatusOK, io.NopCloser(tc.body)), nil
			}))
			t.Cleanup(client.CloseIdleConnections)
			_, err := client.LoadSources(context.Background(), "https://sources.example/private-marker.yaml")
			if err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("metadata error should omit URL, body and transport details: %v", err)
			}
		})
	}
}

func TestClientPreservesContextCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name, expected := "cancelled", context.Canceled
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if deadline {
			name, expected = "deadline exceeded", context.DeadlineExceeded
			ctx, cancel = context.WithDeadline(context.Background(), time.Unix(1, 0))
			defer cancel()
		}
		t.Run(name, func(t *testing.T) {
			client := packages.NewClient(fakes.MetadataTransport(func(request *http.Request) (*http.Response, error) {
				return nil, request.Context().Err()
			}))
			t.Cleanup(client.CloseIdleConnections)
			if _, err := client.LoadSources(ctx, "https://sources.example/private-marker.yaml"); !errors.Is(err, expected) || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("sources cancellation: %v", err)
			}
		})
	}
}
