package webquality_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"commander/apps/agents/web-quality/actions"
	"commander/framework/agent/action"
	"commander/framework/agent/environment"
)

func TestDiscoveryFollowsHostnameAcrossPortsAndRejectsExternalHosts(t *testing.T) {
	// Arrange: same hostname links cross schemes and ports; subdomains stay outside scope.
	var visited []string
	fixtureHTTP(t, func(request *http.Request) string {
		visited = append(visited, request.URL.String())
		switch request.URL.String() {
		case "https://site.test/":
			return `<title>Home</title><a href="http://site.test:8080/about">About</a><a href="http://site.test:8080/about#team">Duplicate</a><a href="https://sub.site.test/">Subdomain</a><a href="https://other.test/">External</a>`
		case "http://site.test:8080/about":
			return `<title>About</title>`
		default:
			t.Errorf("out-of-scope request: %s", request.URL)
			return ""
		}
	})

	// Act: execute the public action with a deterministic HTTP transport.
	result := runAction(t, actions.NewDiscoverPages(), `{"url":"https://site.test","max_pages":3}`)
	var output actions.DiscoverPagesOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}

	// Assert: only in-scope unique pages produce follow-up checks.
	if !reflect.DeepEqual(visited, []string{"https://site.test/", "http://site.test:8080/about"}) || len(output.Pages) != 2 || output.SkippedExternalLinks != 2 || output.Truncated {
		t.Fatalf("visited=%v output=%+v", visited, output)
	}
	checks := map[string]map[string]bool{}
	for _, next := range result.NextActions {
		var page actions.PageInput
		if err := json.Unmarshal(next.Input, &page); err != nil {
			t.Fatal(err)
		}
		if checks[page.URL] == nil {
			checks[page.URL] = map[string]bool{}
		}
		checks[page.URL][next.ActionName] = true
	}
	for _, page := range visited {
		for _, name := range []string{actions.CheckLinksName, actions.CheckAccessibilityName, actions.CapturePageName, actions.CheckSEOName, actions.DetectTechnologiesName} {
			if !checks[page][name] {
				t.Errorf("missing %s proposal for %s", name, page)
			}
		}
	}
}

func TestSEOReportsObservedMetadataAndProblems(t *testing.T) {
	// Arrange: static HTML has an indexing restriction, malformed JSON-LD and no description.
	fixtureHTTP(t, func(*http.Request) string {
		return `<html><head><title>Release preview</title><base href="/docs/"><link rel="canonical" href="intro"><meta name="robots" content="noindex"><script type="application/ld+json">{invalid}</script></head><body><h1>Release preview</h1></body></html>`
	})

	// Act: inspect the public action's structured output.
	result := runAction(t, actions.NewCheckSEO(), `{"url":"https://site.test"}`)
	var output actions.CheckSEOOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}

	// Assert: findings retain evidence and correctly resolve the page's canonical URL.
	if output.Title != "Release preview" || output.CanonicalURL != "https://site.test/docs/intro" || output.H1Count != 1 || output.InvalidJSONLDBlocks != 1 {
		t.Fatalf("unexpected SEO metadata: %+v", output)
	}
	issues := map[string]actions.SEOIssue{}
	for _, issue := range output.Issues {
		issues[issue.Check] = issue
	}
	for _, check := range []string{"missing-meta-description", "indexing-restricted", "invalid-json-ld"} {
		issue := issues[check]
		if len(issue.Evidence) == 0 || issue.Recommendation == "" {
			t.Errorf("%s lacks evidence or recommendation: %+v", check, issue)
		}
	}
	if _, exists := issues["missing-title"]; exists {
		t.Fatal("present title was reported missing")
	}
}

func TestBrowserDiscoveryRespectsExplicitScopeAndRediscovery(t *testing.T) {
	// Arrange: a configured executable is independent of installed host browsers.
	directory := t.TempDir()
	executable := filepath.Join(directory, "browser-fixture")
	if err := os.WriteFile(executable, []byte("fixture executable, never launched"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROMIUM_PATH", executable)
	t.Setenv("CHROMIUM_NO_SANDBOX", "false")
	for _, executor := range []action.Executor{actions.NewCapturePage(), actions.NewCheckAccessibility()} {
		t.Run(executor.Action().Name, func(t *testing.T) {
			// Act: configuration is discovered, then an explicit empty scope denies all paths.
			status, err := executor.Discover(t.Context(), &environment.Local{}, nil)
			if err != nil || status != action.Ready {
				t.Fatalf("configured browser discovery: status=%s error=%v", status, err)
			}
			status, err = executor.Discover(t.Context(), &environment.Local{}, &environment.DiscoveryScope{Paths: []string{}})

			// Assert: stale readiness is cleared and the missing dependency is explained.
			if err != nil || status != action.Unavailable || executor.Action().Status != action.Unavailable || executor.Action().StatusReason == "" {
				t.Fatalf("restricted browser discovery: action=%+v error=%v", executor.Action(), err)
			}
		})
	}
}

func runAction(t *testing.T, executor action.Executor, input string) action.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := executor.Discover(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	result, err := executor.Run(ctx, json.RawMessage(input), []string{"network-access"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func fixtureHTTP(t *testing.T, page func(*http.Request) string) {
	t.Helper()
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = roundTrip(func(request *http.Request) (*http.Response, error) {
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		if request.Method != http.MethodGet {
			return nil, fmt.Errorf("unexpected HTTP method %s", request.Method)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader(page(request))), Request: request}, nil
	})
}
