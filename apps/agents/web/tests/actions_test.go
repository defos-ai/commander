package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/web/actions"
	"github.com/defos-ai/commander/framework/agent/action"
)

func TestDiscoveryFollowsHostnameAcrossPortsAndRejectsExternalHosts(t *testing.T) {
	for _, test := range []struct {
		name     string
		executor func() action.Executor
		checks   []string
	}{
		{"with browser", func() action.Executor { return actions.NewDiscoverPages() }, []string{actions.CheckLinksName, "check-accessibility", "capture-page", actions.CheckSEOName, actions.DetectTechnologiesName}},
		{"without browser", func() action.Executor { return actions.NewDiscoverPagesNoBrowser() }, []string{actions.CheckLinksName, actions.CheckSEOName, actions.DetectTechnologiesName}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CHROMIUM_PATH", "unavailable/browser")
			about := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/about" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				fmt.Fprint(w, "<title>About</title>")
			}))
			defer about.Close()
			home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				fmt.Fprintf(w, `<title>Home</title><a href="%s/about">About</a><a href="%s/about#team">Duplicate</a><a href="https://sub.site.test/">Subdomain</a><a href="https://other.test/">External</a>`, about.URL, about.URL)
			}))
			defer home.Close()
			result := runAction(t, test.executor(), fmt.Sprintf(`{"url":%q,"max_pages":3}`, home.URL))
			var output actions.DiscoverPagesOutput
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatal(err)
			}
			var pages []string
			for _, page := range output.Pages {
				pages = append(pages, page.URL)
			}
			if !reflect.DeepEqual(pages, []string{home.URL + "/", about.URL + "/about"}) || output.SkippedExternalLinks != 2 || output.Truncated {
				t.Fatalf("unexpected discovery: %+v", output)
			}
			if len(result.NextActions) != len(pages)*len(test.checks) {
				t.Fatalf("unexpected checks: %+v", result.NextActions)
			}
			checks := map[string][]string{}
			for _, next := range result.NextActions {
				var page actions.PageInput
				if err := json.Unmarshal(next.Input, &page); err != nil {
					t.Fatal(err)
				}
				checks[page.URL] = append(checks[page.URL], next.ActionName)
			}
			for _, page := range pages {
				if !reflect.DeepEqual(checks[page], test.checks) {
					t.Errorf("checks for %s: %v", page, checks[page])
				}
			}
		})
	}
}

func TestDiscoveryRedirectScope(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<title>Review</title>") }))
	defer destination.Close()
	for _, test := range []struct{ name, destination, wantError string }{
		{"different hostname", "https://other.test/review", "operator approval is required"},
		{"same hostname different port", destination.URL + "/review", ""},
		{"credentials rejected", "https://user:password@127.0.0.1/review", "HTTP(S) URL without credentials"},
		{"non-HTTP scheme rejected", "ftp://127.0.0.1/review", "HTTP(S) URL without credentials"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, test.destination, http.StatusFound) }))
			defer source.Close()
			executor := actions.NewDiscoverPages()
			if _, err := executor.Discover(t.Context(), nil, nil); err != nil {
				t.Fatal(err)
			}
			result, err := executor.Run(t.Context(), json.RawMessage(fmt.Sprintf(`{"url":%q,"max_pages":1}`, source.URL)))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("expected %s, got %v", test.wantError, err)
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
			if len(output.Pages) != 1 || output.Pages[0].URL != test.destination {
				t.Fatalf("unexpected redirect result: %+v", output)
			}
		})
	}
}

func TestSEOReportsObservedMetadataAndProblems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><head><title>Release preview</title><base href="/docs/"><link rel="canonical" href="intro"><meta name="robots" content="noindex"><script type="application/ld+json">{invalid}</script></head><body><h1>Release preview</h1></body></html>`)
	}))
	defer server.Close()
	result := runAction(t, actions.NewCheckSEO(), fmt.Sprintf(`{"url":%q}`, server.URL))
	var output actions.CheckSEOOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.Title != "Release preview" || output.CanonicalURL != server.URL+"/docs/intro" || output.H1Count != 1 || output.InvalidJSONLDBlocks != 1 {
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

func TestHTTPActionsRejectMetadataTargets(t *testing.T) {
	for _, executor := range actions.NewActions() {
		t.Run(executor.Action().Name, func(t *testing.T) {
			if _, err := executor.Discover(t.Context(), nil, nil); err != nil {
				t.Fatal(err)
			}
			_, err := executor.Run(t.Context(), json.RawMessage(`{"url":"http://169.254.169.254/latest/meta-data"}`))
			if err == nil || !strings.Contains(err.Error(), "blocked") {
				t.Fatalf("metadata target was not blocked: %v", err)
			}
		})
	}
}

func TestLinkChecksWithLocalScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<title>Links</title><a href="/missing">Missing</a>`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	result := runAction(t, actions.NewCheckLinks(), fmt.Sprintf(`{"url":%q}`, server.URL))
	var output actions.CheckLinksOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.CheckedLinks != 1 || output.BrokenLinks != 1 || output.Links[0].StatusCode != 404 {
		t.Fatalf("unexpected links: %+v", output)
	}
}

func runAction(t *testing.T, executor action.Executor, input string) action.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := executor.Discover(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	result, err := executor.Run(ctx, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return result
}
