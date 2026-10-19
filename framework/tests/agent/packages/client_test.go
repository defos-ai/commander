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
)

const clientCatalog = "schema_version: 1\nagents:\n  builder:\n    description: Build an agent\n    package: github.com/acme/agents/apps/agents/builder@stable\n"
const clientRelease = `{"tag_name":"v1.2.3","draft":false,"prerelease":false}`

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type metadataReadError struct{}

func (metadataReadError) Read([]byte) (int, error) {
	return 0, errors.New("private-marker body read failure")
}

func metadataResponse(request *http.Request, status int, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, Request: request, ContentLength: -1}
}

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

func TestClientResolvesStableToFreshPins(t *testing.T) {
	calls := 0
	client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
		checkPublicMetadataRequest(t, request)
		if request.URL.String() != "https://api.github.com/repos/acme/agents/releases/latest" {
			t.Fatalf("unexpected latest release endpoint: %s", request.URL)
		}
		versions := []string{"v1.2.3", "v1.3.0"}
		if calls >= len(versions) {
			t.Fatal("resolving an exact saved package contacted the network")
		}
		body := `{"tag_name":"` + versions[calls] + `","draft":false,"prerelease":false,"id":42,"assets":[],"html_url":"https://github.com/acme/agents/releases"}`
		calls++
		return metadataResponse(request, http.StatusOK, io.NopCloser(strings.NewReader(body))), nil
	}))
	t.Cleanup(client.CloseIdleConnections)
	selector := "github.com/acme/agents/apps/agents/builder@stable"
	old, err := client.Resolve(context.Background(), selector)
	if err != nil || old.String() != "github.com/acme/agents/apps/agents/builder@v1.2.3" || old.ReleaseTag() != "apps/agents/builder/v1.2.3" {
		t.Fatalf("first stable resolution: %#v, %v", old, err)
	}
	current, err := client.Resolve(context.Background(), selector)
	if err != nil || current.Version != "v1.3.0" {
		t.Fatalf("stable did not advance: %#v, %v", current, err)
	}
	saved, err := client.Resolve(context.Background(), old.String())
	if err != nil || saved != old || calls != 2 {
		t.Fatalf("saved exact reference changed: %#v, %v; requests: %d", saved, err, calls)
	}
}

func TestClientLoadsConfiguredCatalog(t *testing.T) {
	const source = "https://catalog.example/agents.yaml"
	client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
		checkPublicMetadataRequest(t, request)
		if request.URL.String() != source {
			t.Fatalf("unexpected catalog source: %s", request.URL)
		}
		return metadataResponse(request, http.StatusOK, io.NopCloser(strings.NewReader(clientCatalog))), nil
	}))
	t.Cleanup(client.CloseIdleConnections)
	catalog, err := client.LoadCatalog(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := catalog.Lookup("builder")
	if err != nil || entry.Description != "Build an agent" || entry.Package != "github.com/acme/agents/apps/agents/builder@stable" {
		t.Fatalf("catalog entry: %#v, %v", entry, err)
	}
}

func TestClientRejectsUnsafeSourcesBeforeRequest(t *testing.T) {
	client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("invalid input contacted the network: %s", request.URL)
		return nil, nil
	}))
	t.Cleanup(client.CloseIdleConnections)
	for _, source := range []string{
		"http://catalog.example/private-marker",
		"https://user:private-marker@catalog.example/agents.yaml",
		"https://catalog.example/agents.yaml?token=private-marker",
		"https://catalog.example/agents.yaml#private-marker",
		"https:private-marker",
		"/private-marker",
	} {
		if _, err := client.LoadCatalog(context.Background(), source); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Errorf("unsafe catalog source should fail without echoing input: %v", err)
		}
	}
	if _, err := client.Resolve(context.Background(), "https://private-marker.example/release"); err == nil || strings.Contains(err.Error(), "private-marker") {
		t.Errorf("URL selector should fail without echoing input: %v", err)
	}
}

func TestClientRejectsInvalidLatestRelease(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"malformed JSON", `{"tag_name": private-marker}`},
		{"missing version", `{"draft":false,"prerelease":false}`},
		{"missing draft flag", `{"tag_name":"v1.2.3","prerelease":false}`},
		{"missing prerelease flag", `{"tag_name":"v1.2.3","draft":false}`},
		{"null flag", `{"tag_name":"v1.2.3","draft":null,"prerelease":false}`},
		{"draft", `{"tag_name":"v1.2.3","draft":true,"prerelease":false}`},
		{"prerelease", `{"tag_name":"v1.2.3","draft":false,"prerelease":true}`},
		{"zero version", `{"tag_name":"v0.0.0","draft":false,"prerelease":false}`},
		{"prerelease tag", `{"tag_name":"v1.2.3-rc.1","draft":false,"prerelease":false}`},
		{"module tag", `{"tag_name":"apps/agents/builder/v1.2.3","draft":false,"prerelease":false}`},
		{"trailing JSON", clientRelease + ` {"private-marker":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
				return metadataResponse(request, http.StatusOK, io.NopCloser(strings.NewReader(tc.body))), nil
			}))
			t.Cleanup(client.CloseIdleConnections)
			if ref, err := client.Resolve(context.Background(), "github.com/acme/agents@stable"); err == nil || ref != (packages.Reference{}) || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("invalid latest release resolved: %#v, %v", ref, err)
			}
		})
	}
}

type countedMetadataBody struct {
	io.Reader
	read   int
	closed bool
}

func (b *countedMetadataBody) Read(data []byte) (int, error) {
	n, err := b.Reader.Read(data)
	b.read += n
	return n, err
}

func (b *countedMetadataBody) Close() error {
	b.closed = true
	return nil
}

func TestClientBoundsMetadataAndClosesBodies(t *testing.T) {
	for _, metadata := range []struct {
		name, body string
		limit      int
		load       func(*packages.Client) error
	}{
		{"catalog", clientCatalog, packages.MaxCatalogBytes, func(client *packages.Client) error {
			_, err := client.LoadCatalog(context.Background(), "https://catalog.example/agents.yaml")
			return err
		}},
		{"release", clientRelease, 1 << 20, func(client *packages.Client) error {
			_, err := client.Resolve(context.Background(), "github.com/acme/agents@stable")
			return err
		}},
	} {
		for _, oversized := range []bool{false, true} {
			name, size := metadata.name+" at limit", metadata.limit
			if oversized {
				name, size = metadata.name+" over limit", metadata.limit*2
			}
			t.Run(name, func(t *testing.T) {
				body := &countedMetadataBody{Reader: strings.NewReader(metadata.body + strings.Repeat(" ", size-len(metadata.body)))}
				client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
					return metadataResponse(request, http.StatusOK, body), nil
				}))
				t.Cleanup(client.CloseIdleConnections)
				err := metadata.load(client)
				if (err != nil) != oversized {
					t.Fatalf("size %d, limit %d: %v", size, metadata.limit, err)
				}
				if body.read > metadata.limit+1 || !body.closed {
					t.Fatalf("metadata body read %d bytes, limit %d; closed: %t", body.read, metadata.limit, body.closed)
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
		{"same host redirect", http.StatusFound, "https://catalog.example/private-marker", ""},
		{"cross host redirect", http.StatusTemporaryRedirect, "https://private-marker.example/catalog", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			body := &countedMetadataBody{Reader: strings.NewReader("private-marker response body")}
			client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 {
					t.Fatal("client followed a metadata redirect")
				}
				response := metadataResponse(request, tc.status, body)
				response.Header.Set("Location", tc.location)
				return response, nil
			}))
			t.Cleanup(client.CloseIdleConnections)
			_, err := client.LoadCatalog(context.Background(), "https://catalog.example/private-marker.yaml")
			if err == nil || strings.Contains(err.Error(), "private-marker") || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("unexpected HTTP failure: %v", err)
			}
			if !body.closed {
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
		{"body read", metadataReadError{}, nil},
		{"catalog", strings.NewReader("schema_version: 1\nprivate-marker: true\nagents: {}\n"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return metadataResponse(request, http.StatusOK, io.NopCloser(tc.body)), nil
			}))
			t.Cleanup(client.CloseIdleConnections)
			_, err := client.LoadCatalog(context.Background(), "https://catalog.example/private-marker.yaml")
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
			client := packages.NewClient(metadataTransport(func(request *http.Request) (*http.Response, error) {
				return nil, request.Context().Err()
			}))
			t.Cleanup(client.CloseIdleConnections)
			if _, err := client.LoadCatalog(ctx, "https://catalog.example/private-marker.yaml"); !errors.Is(err, expected) || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("catalog cancellation: %v", err)
			}
			if _, err := client.Resolve(ctx, "github.com/acme/agents@stable"); !errors.Is(err, expected) {
				t.Fatalf("release cancellation: %v", err)
			}
		})
	}
}
