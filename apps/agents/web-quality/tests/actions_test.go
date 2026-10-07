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

	"github.com/defos-ai/commander/apps/agents/web-quality/actions"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

func TestDiscoveryFollowsHostnameAcrossPortsAndRejectsExternalHosts(t *testing.T) {
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

	result := runAction(t, actions.NewDiscoverPages(), `{"url":"https://site.test","max_pages":3}`)
	var output actions.DiscoverPagesOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}

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

func TestDiscoveryRedirectScope(t *testing.T) {
	const source = "https://site.test/"
	for _, test := range []struct {
		name        string
		destination string
		wantError   []string
	}{
		{
			name: "different hostname requires operator approval", destination: "https://other.test/review",
			wantError: []string{`redirect to "https://other.test/review"`, `website scope "site.test"`, `operator approval is required`, `hostname "other.test"`},
		},
		{
			name: "subdomain requires operator approval", destination: "https://www.site.test/review",
			wantError: []string{`redirect to "https://www.site.test/review"`, `website scope "site.test"`, `operator approval is required`, `hostname "www.site.test"`},
		},
		{name: "same hostname different scheme", destination: "http://site.test/review"},
		{name: "same hostname different port", destination: "https://site.test:8443/review"},
		{name: "same hostname different scheme and port", destination: "http://site.test:8080/review"},
		{name: "credentials rejected", destination: "https://user:password@site.test/review", wantError: []string{"HTTP(S) URL without credentials"}},
		{name: "non-HTTP scheme rejected", destination: "ftp://site.test/review", wantError: []string{"HTTP(S) URL without credentials"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var visited []string
			original := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = original })
			http.DefaultTransport = roundTrip(func(request *http.Request) (*http.Response, error) {
				visited = append(visited, request.URL.String())
				status := http.StatusOK
				headers := http.Header{"Content-Type": {"text/html"}}
				if request.URL.String() == source {
					status = http.StatusFound
					headers.Set("Location", test.destination)
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader("<title>Review</title>")), Request: request}, nil
			})
			executor := actions.NewDiscoverPages()
			if _, err := executor.Discover(t.Context(), nil, nil); err != nil {
				t.Fatal(err)
			}

			result, err := executor.Run(t.Context(), json.RawMessage(`{"url":"https://site.test","max_pages":1}`), []string{"network-access"})

			if len(test.wantError) > 0 {
				if err == nil {
					t.Fatal("expected redirect to be rejected")
				}
				for _, message := range test.wantError {
					if !strings.Contains(err.Error(), message) {
						t.Errorf("error %q does not contain %q", err, message)
					}
				}
				if !reflect.DeepEqual(visited, []string{source}) {
					t.Fatalf("fetched blocked redirect destination: %v", visited)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var output actions.DiscoverPagesOutput
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(visited, []string{source, test.destination}) || len(output.Pages) != 1 || output.Pages[0].URL != test.destination {
				t.Fatalf("same-hostname redirect was not discovered: visited=%v output=%+v", visited, output)
			}
		})
	}
}

func TestSEOReportsObservedMetadataAndProblems(t *testing.T) {
	fixtureHTTP(t, func(*http.Request) string {
		return `<html><head><title>Release preview</title><base href="/docs/"><link rel="canonical" href="intro"><meta name="robots" content="noindex"><script type="application/ld+json">{invalid}</script></head><body><h1>Release preview</h1></body></html>`
	})

	result := runAction(t, actions.NewCheckSEO(), `{"url":"https://site.test"}`)
	var output actions.CheckSEOOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}

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
	directory := t.TempDir()
	executable := filepath.Join(directory, "browser-fixture")
	if err := os.WriteFile(executable, []byte("fixture executable, never launched"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROMIUM_PATH", executable)
	t.Setenv("CHROMIUM_NO_SANDBOX", "false")
	for _, executor := range []action.Executor{actions.NewCapturePage(), actions.NewCheckAccessibility()} {
		t.Run(executor.Action().Name, func(t *testing.T) {
			status, err := executor.Discover(t.Context(), &environment.Local{}, nil)
			if err != nil || status != action.Ready {
				t.Fatalf("configured browser discovery: status=%s error=%v", status, err)
			}
			status, err = executor.Discover(t.Context(), &environment.Local{}, &environment.DiscoveryScope{Paths: []string{}})

			if err != nil || status != action.Unavailable || executor.Action().Status != action.Unavailable || executor.Action().StatusReason == "" {
				t.Fatalf("restricted browser discovery: action=%+v error=%v", executor.Action(), err)
			}
			missing := executor.Action().MissingRequirements
			if len(missing) != 1 || missing[0] != (actionmodel.Requirement{Kind: actionmodel.ExecutableRequirement, Name: "chromium"}) {
				t.Fatalf("missing structured dependency: %+v", missing)
			}
			missing[0].Name = "caller mutation"
			if executor.Action().MissingRequirements[0].Name != "chromium" {
				t.Fatal("action snapshot shares mutable requirements")
			}
			status, err = executor.Discover(t.Context(), &environment.Local{}, nil)
			if err != nil || status != action.Ready || len(executor.Action().MissingRequirements) != 0 {
				t.Fatalf("rediscovery retained missing requirements: %+v, %v", executor.Action(), err)
			}
		})
	}
}

func TestBrowserDiscoveryReportsInvalidConfigurationAsRequirement(t *testing.T) {
	t.Setenv("CHROMIUM_PATH", "relative/browser")
	executor := actions.NewCapturePage()

	status, err := executor.Discover(t.Context(), &environment.Local{}, nil)

	missing := executor.Action().MissingRequirements
	if err != nil || status != action.Unavailable || len(missing) != 1 || missing[0] != (actionmodel.Requirement{Kind: actionmodel.ConfigurationRequirement, Name: "CHROMIUM_PATH"}) {
		t.Fatalf("invalid configuration was not reported as a readiness gap: %+v, %v", executor.Action(), err)
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
