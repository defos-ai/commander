package actions_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	web "commander/apps/agents/web-quality/actions"
)

func TestDetectTechnologiesReportsDeclarationsWithoutFetchingScripts(t *testing.T) {
	// Arrange: explicit header, generator, package, and framework declarations.
	var scriptCalls atomic.Int32
	scripts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { scriptCalls.Add(1) }))
	defer scripts.Close()
	var pageCalls atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageCalls.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Server", "nginx/1.26.0")
		w.Header().Set("X-Powered-By", "PHP/8.3.8")
		fmt.Fprintf(w, `<meta name="generator" content="WordPress 6.6.2"><script src="%s/jquery-3.7.1.min.js"></script><script src="/npm/bootstrap@5.3.3/dist/js/bootstrap.bundle.min.js"></script><script src="/npm/react@18.3.1/umd/react.production.min.js"></script><script id="__NEXT_DATA__" type="application/json">{"buildId":"not-a-version"}</script><main ng-version="18.2.0"></main><script>fetch('/executed')</script>`, scripts.URL)
	}))
	defer site.Close()
	a := web.NewDetectTechnologies()
	ready(t, a)
	// Act: only network permission is needed; Chromium is not involved.
	raw, _ := json.Marshal(web.PageInput{URL: site.URL})
	result, err := a.Run(context.Background(), raw, []string{"network-access"})
	if err != nil {
		t.Fatal(err)
	}
	out := decode[web.DetectTechnologiesOutput](t, result)
	// Assert: every finding carries an observed declaration and its source.
	for _, want := range []struct{ name, version, source string }{
		{"nginx", "1.26.0", "response-header:Server"},
		{"PHP", "8.3.8", "response-header:X-Powered-By"},
		{"WordPress", "6.6.2", "meta-generator"},
		{"jQuery", "3.7.1", "script-src"},
		{"Bootstrap", "5.3.3", "script-src"},
		{"React", "18.3.1", "script-src"},
		{"Next.js", "", "html-marker"},
		{"Angular", "18.2.0", "html-marker"},
	} {
		found := false
		for _, got := range out.Indicators {
			if got.Name == want.name && got.Version == want.version && got.Source == want.source && got.Evidence != "" {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %+v: %+v", want, out.Indicators)
		}
	}
	if len(out.Indicators) != 8 || len(out.ServerHeaders) != 2 || out.StatusCode != 200 || !out.IsHTML || out.Truncated || out.ScriptsInspected != 3 {
		t.Fatalf("unexpected inspection: %+v", out)
	}
	if !strings.Contains(out.Scope, "declared indicators") || len(out.Limitations) == 0 {
		t.Fatalf("missing diagnostic limits: %+v", out)
	}
	if pageCalls.Load() != 1 || scriptCalls.Load() != 0 {
		t.Fatalf("inspection loaded extra resources: page requests=%d, script requests=%d", pageCalls.Load(), scriptCalls.Load())
	}
}

func TestDetectTechnologiesAvoidsSubstringAndQueryVersionGuesses(t *testing.T) {
	// Arrange: package names in unrelated filenames, text, attributes and query strings.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<p>React WordPress jQuery 1.0</p><script>const jquery = "1.0"; const React = {};</script><script src="/reactive.js"></script><script src="/not-jquery-3.7.1.min.js"></script><script src="/bootstrap-theme.js"></script><script src="/jquery.plugin.min.js"></script><script src="/application.js?package=vue&amp;version=3.4.0"></script><script src="/jquery.min.js?v=9.9.9"></script><script type="text/template" src="/bootstrap-4.0.0.js"></script><template data-reactroot><script src="/react-18.2.0.js"></script></template><svg data-v-app><script src="/vue-3.4.0.js"></script></svg><meta name="description" content="WordPress 6.6.2"><div id="__NEXT_DATA__"></div><div ng-version="current"></div>`)
	}))
	defer site.Close()
	a := web.NewDetectTechnologies()
	ready(t, a)
	// Act.
	out := decode[web.DetectTechnologiesOutput](t, run(t, a, web.PageInput{URL: site.URL}))
	// Assert: a filename declaration is evidence, while a cache query is not a version.
	if len(out.Indicators) != 1 || out.Indicators[0].Name != "jQuery" || out.Indicators[0].Version != "" || out.Truncated {
		t.Fatalf("guessed technology or version: %+v", out)
	}
}

func TestDetectTechnologiesPreservesExplicitPrereleaseAndConflictingVersions(t *testing.T) {
	// Arrange: a prerelease filename and inconsistent path/filename declarations.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<script src="/jquery-4.0.0-rc.1.min.js"></script><script src="/bootstrap@5.3.3/bootstrap-4.6.2.min.js"></script>`)
	}))
	defer site.Close()
	a := web.NewDetectTechnologies()
	ready(t, a)
	// Act.
	out := decode[web.DetectTechnologiesOutput](t, run(t, a, web.PageInput{URL: site.URL}))
	// Assert: preserve the actual prerelease and both conflicting declarations.
	versions := map[string]bool{}
	for _, indicator := range out.Indicators {
		versions[indicator.Name+"@"+indicator.Version] = true
	}
	if len(out.Indicators) != 3 || !versions["jQuery@4.0.0-rc.1"] || !versions["Bootstrap@5.3.3"] || !versions["Bootstrap@4.6.2"] {
		t.Fatalf("misreported explicit versions: %+v", out.Indicators)
	}
}

func TestDetectTechnologiesRedactsURLsAndSelectsHeaders(t *testing.T) {
	// Arrange: secrets in each supported URL-bearing evidence location.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Server", "nginx/1.26.0 (https://server-user:server-password@example.test/help?header-secret=yes#header-fragment)")
		w.Header().Set("Set-Cookie", "session=cookie-secret")
		w.Header().Set("Authorization", "Bearer authorization-secret")
		w.Header().Set("X-Private-Header", "private-header-secret")
		fmt.Fprint(w, `<meta name="generator" content="WordPress 6.6.2 https://generator-user:generator-password@example.test/?generator-secret=yes#generator-fragment"><script src="https://script-user:script-password@example.test/jquery-3.7.1.min.js?script-secret=yes#script-fragment"></script><script src="/bootstrap.min.js?relative-secret=yes#relative-fragment"></script>`)
	}))
	defer site.Close()
	a := web.NewDetectTechnologies()
	ready(t, a)
	// Act.
	result := run(t, a, web.PageInput{URL: site.URL + "/?page-secret=yes#page-fragment"})
	out := decode[web.DetectTechnologiesOutput](t, result)
	// Assert: raw response headers and URL secrets do not enter the action result.
	for _, secret := range []string{"server-user", "server-password", "header-secret", "header-fragment", "cookie-secret", "authorization-secret", "private-header-secret", "generator-user", "generator-password", "generator-secret", "generator-fragment", "script-user", "script-password", "script-secret", "script-fragment", "relative-secret", "relative-fragment", "page-secret", "page-fragment"} {
		if strings.Contains(string(result.Output), secret) {
			t.Errorf("output exposed %q: %s", secret, result.Output)
		}
	}
	if len(out.ServerHeaders) != 1 || out.ServerHeaders[0].Name != "Server" || len(out.Indicators) != 4 || out.URL != site.URL+"/" {
		t.Fatalf("unexpected sanitized output: %+v", out)
	}
}

func TestDetectTechnologiesBoundsReportsAndTraversal(t *testing.T) {
	for _, fixture := range []struct {
		name string
		html string
	}{
		{"scripts", strings.Repeat(`<script src="/unknown.js"></script>`, 201)},
		{"nodes", strings.Repeat(`<div></div>`, 20001)},
		{"long-script", `<script src="/` + strings.Repeat("a", 4096) + `/jquery.min.js"></script>`},
		{"long-generator", `<meta name="generator" content="WordPress 6.6.2 ` + strings.Repeat("a", 400) + `">`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// Arrange.
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, fixture.html)
			}))
			defer site.Close()
			a := web.NewDetectTechnologies()
			ready(t, a)
			// Act.
			out := decode[web.DetectTechnologiesOutput](t, run(t, a, web.PageInput{URL: site.URL}))
			// Assert.
			if !out.Truncated || out.ScriptsInspected > 200 {
				t.Fatalf("missing inspection bounds: %+v", out)
			}
			for _, indicator := range out.Indicators {
				if len([]rune(indicator.Evidence)) > 240 || len([]rune(indicator.Name)) > 80 || len([]rune(indicator.Version)) > 40 {
					t.Errorf("unbounded indicator: %+v", indicator)
				}
			}
		})
	}
}

func TestDetectTechnologiesBoundsIndicatorsAndHeaderValues(t *testing.T) {
	// Arrange: many distinct declarations with oversized selected header values.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		for i := 0; i < 10; i++ {
			w.Header().Add("Server", "nginx/1.26.0 "+strings.Repeat("a", 400))
		}
		for i := 0; i < 70; i++ {
			fmt.Fprintf(w, `<script src="/jquery-3.7.%d.min.js"></script>`, i)
		}
	}))
	defer site.Close()
	a := web.NewDetectTechnologies()
	ready(t, a)
	// Act.
	out := decode[web.DetectTechnologiesOutput](t, run(t, a, web.PageInput{URL: site.URL}))
	// Assert.
	if !out.Truncated || len(out.Indicators) != 48 || len(out.ServerHeaders) != 8 || out.ScriptsInspected != 70 {
		t.Fatalf("missing output bounds: %+v", out)
	}
	for _, header := range out.ServerHeaders {
		if len([]rune(header.Value)) > 240 {
			t.Fatalf("unbounded header: %+v", header)
		}
	}
}

func TestDetectTechnologiesDoesNotParseNonHTML(t *testing.T) {
	// Arrange: HTML-like text in a JSON response is not a page declaration.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Server", "cloudflare")
		fmt.Fprint(w, `{"example":"<script src='/jquery-3.7.1.min.js'></script>"}`)
	}))
	defer site.Close()
	a := web.NewDetectTechnologies()
	ready(t, a)
	// Act.
	out := decode[web.DetectTechnologiesOutput](t, run(t, a, web.PageInput{URL: site.URL}))
	// Assert: response headers remain available even when the document is not HTML.
	if out.IsHTML || out.ScriptsInspected != 0 || len(out.Indicators) != 1 || out.Indicators[0].Name != "cloudflare" || out.Indicators[0].Version != "" {
		t.Fatalf("unexpected non-HTML findings: %+v", out)
	}
}

func TestDetectTechnologiesRejectsOffHostnameRedirectsAndUnauthorizedRequests(t *testing.T) {
	// Arrange.
	var outsideCalls atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outsideCalls.Add(1) }))
	defer outside.Close()
	var calls atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, strings.Replace(outside.URL, "127.0.0.1", "localhost", 1)+"/?redirect-secret=yes", http.StatusFound)
	}))
	defer site.Close()
	a := web.NewDetectTechnologies()
	ready(t, a)
	raw, _ := json.Marshal(web.PageInput{URL: site.URL + "/?request-secret=yes"})
	// Act and assert: reject before networking when unauthorized, and retain redirect scope.
	if _, err := a.Run(context.Background(), raw, nil); err == nil || calls.Load() != 0 {
		t.Fatalf("request without permission: error=%v, calls=%d", err, calls.Load())
	}
	_, err := a.Run(context.Background(), raw, grants)
	if err == nil || outsideCalls.Load() != 0 || calls.Load() != 1 {
		t.Fatalf("off-hostname redirect was not rejected: error=%v, outside=%d, calls=%d", err, outsideCalls.Load(), calls.Load())
	}
	if strings.Contains(err.Error(), "request-secret") || strings.Contains(err.Error(), "redirect-secret") {
		t.Fatalf("request error exposed query secrets: %v", err)
	}
}
