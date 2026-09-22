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

func TestCheckSEOMetadata(t *testing.T) {
	// Arrange: metadata references external resources, which the static check must not request.
	var requests atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head>
<title>Release &amp; preview</title>
<meta NAME="Description" content="A page-specific summary.">
<base href="/articles/"><base href="/ignored/">
<link rel="alternate CANONICAL" href="release">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="index, follow">
<meta property="og:title" content="Release preview">
<meta property="og:type" content="article">
<meta property="og:image" content="https://outside.example/image.png">
<meta property="og:url" content="https://outside.example/release">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Article","headline":"Release preview"}</script>
</head><body><h1>Release <span>preview</span></h1>
<svg><title>Decorative SVG title</title></svg>
<template><h1>Template heading</h1><script type="application/ld+json">invalid</script></template>
<script>document.title='Rendered title';</script></body></html>`)
	}))
	defer site.Close()
	a := web.NewCheckSEO()
	ready(t, a)
	// Act.
	out := decode[web.CheckSEOOutput](t, run(t, a, web.PageInput{URL: site.URL + "/original"}))
	// Assert: head metadata, first base URL and static heading text are extracted without rendering or extra requests.
	if out.Title != "Release & preview" || out.TitleCount != 1 || out.MetaDescription != "A page-specific summary." || out.MetaDescriptionCount != 1 {
		t.Fatalf("unexpected page metadata: %+v", out)
	}
	if out.CanonicalURL != site.URL+"/articles/release" || out.CanonicalCount != 1 || out.H1Count != 1 || len(out.H1s) != 1 || out.H1s[0] != "Release preview" {
		t.Fatalf("unexpected canonical/headings: %+v", out)
	}
	if out.StructuredDataBlocks != 1 || out.InvalidJSONLDBlocks != 0 || len(out.OpenGraph) != 4 || len(out.Robots) != 1 || len(out.Issues) != 0 || out.Truncated {
		t.Fatalf("unexpected metadata findings: %+v", out)
	}
	if requests.Load() != 1 || !strings.Contains(out.Scope, "no JavaScript rendering") || !strings.Contains(out.Scope, "not schema validity") {
		t.Fatalf("missing scope or unexpected requests: %+v, requests=%d", out, requests.Load())
	}
}

func TestCheckSEOMissingMetadata(t *testing.T) {
	// Arrange: SVG and inert template content must not hide missing document metadata.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title> </title><meta name="description" content=" "></head><body><svg><title>SVG</title></svg><template><h1>Hidden</h1></template></body></html>`)
	}))
	defer site.Close()
	a := web.NewCheckSEO()
	ready(t, a)
	// Act.
	out := decode[web.CheckSEOOutput](t, run(t, a, web.PageInput{URL: site.URL}))
	// Assert: findings contain actionable evidence without inferring ranking or indexing status.
	issues := seoIssues(out)
	for _, name := range []string{"missing-title", "missing-meta-description", "missing-canonical", "missing-h1", "missing-viewport", "incomplete-open-graph"} {
		issue, ok := issues[name]
		if !ok || len(issue.Evidence) == 0 || issue.Recommendation == "" || issue.Severity == "" {
			t.Errorf("missing useful %s finding: %+v", name, out)
		}
	}
	if _, ok := issues["indexing-restricted"]; ok {
		t.Fatal("inferred an indexing restriction without a directive")
	}
	if out.StructuredDataBlocks != 0 || out.InvalidJSONLDBlocks != 0 {
		t.Fatalf("fabricated structured data: %+v", out)
	}
}

func TestCheckSEORobotsAndMalformedMetadata(t *testing.T) {
	// Arrange: bot-specific header directives coexist with meta tags and duplicate metadata.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Add("X-Robots-Tag", "googlebot: NOINDEX, nofollow")
		w.Header().Add("X-Robots-Tag", "max-snippet: 100")
		fmt.Fprint(w, `<html><head><title>First</title><title>Second</title>
<meta name="description" content="First summary"><meta name="description" content="Second summary">
<link rel="canonical" href="javascript:alert(1)"><link rel="canonical" href="/second">
<meta name="robots" content="NONE"><meta name="bingbot" content="noindex">
</head><body><h1></h1><h1>Second heading</h1>
<script type="application/ld+json">{"broken":</script>
<script type="application/ld+json">null</script>
<script type="application/ld+json">[{"@type":"Thing"}]</script></body></html>`)
	}))
	defer site.Close()
	a := web.NewCheckSEO()
	ready(t, a)
	// Act.
	out := decode[web.CheckSEOOutput](t, run(t, a, web.PageInput{URL: site.URL}))
	// Assert: declarations remain observable and malformed values are reported contextually.
	issues := seoIssues(out)
	for _, name := range []string{"duplicate-title", "duplicate-meta-description", "duplicate-canonical", "invalid-canonical", "empty-h1", "multiple-h1", "indexing-restricted", "invalid-json-ld"} {
		if _, ok := issues[name]; !ok {
			t.Errorf("missing %s: %+v", name, out)
		}
	}
	if out.StructuredDataBlocks != 3 || out.InvalidJSONLDBlocks != 2 || out.CanonicalURL != "" || len(out.Robots) != 4 {
		t.Fatalf("unexpected metadata: %+v", out)
	}
	restriction := issues["indexing-restricted"]
	if restriction.Severity != "info" || len(restriction.Evidence) != 3 || !strings.Contains(restriction.Recommendation, "intentional") || !strings.Contains(restriction.Evidence[0], "X-Robots-Tag") {
		t.Fatalf("missing contextual indexing evidence: %+v", restriction)
	}
}

func TestCheckSEOCanonicalRedirectAndScope(t *testing.T) {
	// Arrange: relative canonical URLs resolve against the final URL; external targets remain unfetched.
	var requests atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final/page", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/external" {
			fmt.Fprint(w, `<link rel="canonical" href="https://outside.example/article"><meta name="robots" content="notnoindex">`)
			return
		}
		fmt.Fprint(w, `<link rel="canonical" href="preferred">`)
	}))
	defer site.Close()
	a := web.NewCheckSEO()
	ready(t, a)
	// Act.
	redirected := decode[web.CheckSEOOutput](t, run(t, a, web.PageInput{URL: site.URL + "/start"}))
	external := decode[web.CheckSEOOutput](t, run(t, a, web.PageInput{URL: site.URL + "/external"}))
	// Assert.
	if redirected.URL != site.URL+"/final/page" || redirected.CanonicalURL != site.URL+"/final/preferred" || requests.Load() != 3 {
		t.Fatalf("unexpected canonical resolution or requests: %+v, requests=%d", redirected, requests.Load())
	}
	issues := seoIssues(external)
	if issue, ok := issues["external-canonical"]; !ok || issue.Severity != "info" || !strings.Contains(issue.Recommendation, "intentional") {
		t.Fatalf("missing contextual external canonical finding: %+v", external)
	}
	if _, ok := issues["indexing-restricted"]; ok {
		t.Fatal("matched a robots directive substring")
	}
}

func TestCheckSEOValidatesEveryCanonical(t *testing.T) {
	// Arrange: the first declaration is usable, but later declarations are not.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<link rel="canonical" href="/preferred"><link rel="canonical" href="javascript:bad"><link rel="canonical" href="">`)
	}))
	defer site.Close()
	a := web.NewCheckSEO()
	ready(t, a)
	// Act.
	out := decode[web.CheckSEOOutput](t, run(t, a, web.PageInput{URL: site.URL}))
	// Assert: preserving the first value does not hide later invalid declarations.
	issue, ok := seoIssues(out)["invalid-canonical"]
	if out.CanonicalURL != site.URL+"/preferred" || out.CanonicalCount != 3 || !ok || len(issue.Evidence) != 2 || !strings.Contains(issue.Evidence[0], "declaration 2") {
		t.Fatalf("missed invalid duplicate canonicals: %+v", out)
	}
}

func TestCheckSEOBoundedEvidence(t *testing.T) {
	// Arrange: many headings, metadata values and invalid JSON-LD blocks within the response-size limit.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>%s</title>`, strings.Repeat("界", 2000))
		for i := 0; i < 30; i++ {
			fmt.Fprintf(w, `<meta property="og:custom%d" content="%s"><meta name="robots" content="noindex">`, i, strings.Repeat("x", 2000))
		}
		fmt.Fprint(w, `</head><body>`)
		for i := 0; i < 30; i++ {
			fmt.Fprintf(w, `<h1>%s</h1><script type="application/ld+json">bad</script>`, strings.Repeat("x", 2000))
		}
		fmt.Fprint(w, `</body></html>`)
	}))
	defer site.Close()
	a := web.NewCheckSEO()
	ready(t, a)
	// Act.
	result := run(t, a, web.PageInput{URL: site.URL})
	out := decode[web.CheckSEOOutput](t, result)
	// Assert: full counts remain useful while the returned evidence is bounded and valid Unicode.
	if !out.Truncated || len([]rune(out.Title)) != 301 || out.H1Count != 30 || len(out.H1s) != 3 || len(out.Robots) != 10 || len(out.OpenGraph) != 10 || out.InvalidJSONLDBlocks != 30 {
		t.Fatalf("unbounded/missing metadata counts: %+v", out)
	}
	if len(result.Output) > 20000 || strings.Contains(string(result.Output), `\ufffd`) {
		t.Fatalf("oversized output or damaged Unicode: %d bytes", len(result.Output))
	}
	for _, issue := range out.Issues {
		if len(issue.Evidence) > 3 {
			t.Fatalf("unbounded evidence: %+v", issue)
		}
	}
}

func TestCheckSEOErrorsAndPermissions(t *testing.T) {
	// Arrange: responses that cannot support static HTML analysis.
	var requests atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/missing":
			http.NotFound(w, r)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		case "/large":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, strings.Repeat("x", (2<<20)+1))
		}
	}))
	defer site.Close()
	a := web.NewCheckSEO()
	ready(t, a)
	// Act and assert: clear failures preserve existing permissions, URL validation and response limits.
	for _, test := range []struct{ path, want string }{{"/missing", "HTTP 404"}, {"/json", "HTML"}, {"/large", "2 MiB"}} {
		raw, _ := json.Marshal(web.PageInput{URL: site.URL + test.path})
		if _, err := a.Run(context.Background(), raw, grants); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s: expected %s error, got %v", test.path, test.want, err)
		}
	}
	requestsBefore := requests.Load()
	raw, _ := json.Marshal(web.PageInput{URL: site.URL})
	if _, err := a.Run(context.Background(), raw, nil); err == nil {
		t.Fatal("ran without network permission")
	}
	for _, raw := range []string{`{"url":"file:///etc/passwd"}`, `{"url":"http://user:secret@example.test"}`, `{"url":null}`, `{"url":"http://example.test","script":"alert(1)"}`} {
		if _, err := a.Run(context.Background(), json.RawMessage(raw), grants); err == nil {
			t.Errorf("accepted invalid input %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Run(ctx, raw, grants); err == nil {
		t.Fatal("ignored cancellation")
	}
	if requests.Load() != requestsBefore {
		t.Fatal("requested a URL despite denied permission, invalid input or cancellation")
	}
}

func seoIssues(out web.CheckSEOOutput) map[string]web.SEOIssue {
	issues := map[string]web.SEOIssue{}
	for _, issue := range out.Issues {
		issues[issue.Check] = issue
	}
	return issues
}
