package actions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	web "commander/apps/agents/web-quality/actions"
	"commander/framework/agent/action"
	"commander/framework/agent/environment"
)

var grants = []string{"network-access", "execute-command"}

func ready(t *testing.T, a action.Executor) {
	t.Helper()
	status, err := a.Discover(context.Background(), &environment.Local{}, nil)
	if err != nil || status != action.Ready {
		t.Fatalf("discover %s: %s, %v", a.Action().Name, status, err)
	}
}
func run(t *testing.T, a action.Executor, input any) action.Result {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Run(context.Background(), raw, grants)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func decode[T any](t *testing.T, result action.Result) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(result.Output, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestDiscoverPagesAndCheckLinks(t *testing.T) {
	// Arrange: a small website with one missing link, duplicate anchors, and an external target.
	var externalCalls atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalCalls.Add(1) }))
	defer external.Close()
	website := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprintf(w, `<title>Release preview</title><a href="/about">About</a><a href="/about#team">Team</a><a href="/missing">Missing</a><a href="%s">External</a><a href="mailto:team@example.test">Email</a>`, strings.Replace(external.URL, "127.0.0.1", "localhost", 1))
		case "/about":
			fmt.Fprint(w, `<title>About</title><a href="/">Home</a>`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer website.Close()
	discover, links := web.NewDiscoverPages(), web.NewCheckLinks()
	ready(t, discover)
	ready(t, links)
	// Act.
	result := run(t, discover, web.DiscoverPagesInput{URL: website.URL, MaxPages: 2})
	pages := decode[web.DiscoverPagesOutput](t, result)
	checked := decode[web.CheckLinksOutput](t, run(t, links, web.PageInput{URL: website.URL}))
	// Assert: capped automatic fan-out and useful findings, with no off-hostname request.
	if len(pages.Pages) != 2 || !pages.Truncated || len(result.NextActions) != 10 {
		t.Fatalf("unexpected discovery: %+v, proposals=%d", pages, len(result.NextActions))
	}
	for i, next := range result.NextActions {
		if next.ActionName != []string{web.CheckLinksName, web.CheckAccessibilityName, web.CapturePageName, web.CheckSEOName, web.DetectTechnologiesName}[i%5] {
			t.Fatalf("unexpected follow-up: %+v", next)
		}
	}
	if checked.Title != "Release preview" || checked.CheckedLinks != 2 || checked.BrokenLinks != 1 || checked.SkippedExternalLinks != 1 || checked.SkippedNonHTTPLinks != 1 {
		t.Fatalf("unexpected links: %+v", checked)
	}
	if externalCalls.Load() != 0 {
		t.Fatal("requested external links")
	}
}

func TestActionLimitsAndPermissions(t *testing.T) {
	// Arrange.
	discover := web.NewDiscoverPages()
	ready(t, discover)
	// Act and assert: malformed inputs fail before any request.
	for _, raw := range []string{`{"url":"file:///etc/passwd"}`, `{"url":"http://user:secret@example.test"}`, `{"url":"http://example.test","max_pages":11}`, `{"url":"http://example.test","max_pages":0}`, `{"url":null}`, `{"url":"http://example.test","script":"alert(1)"}`} {
		if _, err := discover.Run(context.Background(), json.RawMessage(raw), grants); err == nil {
			t.Errorf("accepted invalid input %s", raw)
		}
	}
	if _, err := discover.Run(context.Background(), json.RawMessage(`{"url":"http://example.test"}`), nil); err == nil {
		t.Fatal("ran without network permission")
	}
}

func TestLinkRedirectsAndLimits(t *testing.T) {
	// Arrange.
	var externalCalls atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalCalls.Add(1) }))
	defer external.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<a href="/redirect">Redirect</a>`)
			for i := 0; i < 50; i++ {
				fmt.Fprintf(w, `<a href="/item/%d">Item</a>`, i)
			}
		case "/redirect":
			http.Redirect(w, r, strings.Replace(external.URL, "127.0.0.1", "localhost", 1), 302)
		default:
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	links := web.NewCheckLinks()
	ready(t, links)
	// Act.
	out := decode[web.CheckLinksOutput](t, run(t, links, web.PageInput{URL: server.URL}))
	// Assert.
	if out.CheckedLinks != 40 || !out.Truncated || out.BrokenLinks != 1 || externalCalls.Load() != 0 {
		t.Fatalf("scope/limits not respected: %+v, external=%d", out, externalCalls.Load())
	}
}

func TestBodyLimitAndCancellation(t *testing.T) {
	// Arrange.
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", (2<<20)+1)) }))
	defer large.Close()
	links := web.NewCheckLinks()
	ready(t, links)
	raw, _ := json.Marshal(web.PageInput{URL: large.URL})
	// Act and assert.
	if _, err := links.Run(context.Background(), raw, grants); err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Fatalf("expected body limit, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := links.Run(ctx, raw, grants); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestBrowserActions(t *testing.T) {
	// Arrange: render real HTML/JS; fixture includes known basic issues and mobile overflow.
	capture, audit := web.NewCapturePage(), web.NewCheckAccessibility()
	for _, a := range []action.Executor{capture, audit} {
		status, err := a.Discover(context.Background(), &environment.Local{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if status != action.Ready {
			if os.Getenv("CHROMIUM_PATH") != "" {
				t.Fatal("configured browser was not discovered")
			}
			t.Skip("Chromium not installed; set CHROMIUM_PATH to run browser checks")
		}
	}
	var postCalls atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			postCalls.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Preview</title><style>main{min-width:700px;height:200px;background:#ddd}</style></head><body><main><h1>Welcome</h1><img src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="><input><button></button><a href="/more"></a></main><script>document.title='Rendered preview';fetch('/write',{method:'POST'}).catch(()=>{});throw new Error('Fixture error')</script></body></html>`)
	}))
	defer site.Close()
	// Act.
	screenshotResult := run(t, capture, web.PageInput{URL: site.URL})
	screens := decode[web.CapturePageOutput](t, screenshotResult)
	auditResult := decode[web.CheckAccessibilityOutput](t, run(t, audit, web.PageInput{URL: site.URL}))
	// Assert: browser-rendered content, actual bounded PNG artifacts, explicit check limits.
	if screens.Title != "Rendered preview" || len(screenshotResult.Artifacts) != 2 || !screens.Viewports[1].HorizontalOverflow {
		t.Fatalf("unexpected capture: %+v", screens)
	}
	for i, file := range screenshotResult.Artifacts {
		if err := file.Validate(); err != nil {
			t.Fatal(err)
		}
		size, err := png.DecodeConfig(bytes.NewReader(file.Data))
		if err != nil {
			t.Fatal(err)
		}
		if size.Width != screens.Viewports[i].Width || size.Height != screens.Viewports[i].Height {
			t.Fatalf("unexpected image dimensions: %+v", size)
		}
	}
	checks := map[string]bool{}
	for _, issue := range auditResult.Issues {
		checks[issue.Check] = true
	}
	for _, name := range []string{"missing-language", "image-missing-alt", "control-missing-label", "button-missing-name", "link-missing-name"} {
		if !checks[name] {
			t.Errorf("missing expected %s: %+v", name, auditResult)
		}
	}
	if auditResult.Scope == "" || len(auditResult.BrowserErrors) == 0 {
		t.Fatalf("missing scope/errors: %+v", auditResult)
	}
	if postCalls.Load() != 0 {
		t.Fatal("browser submitted a write request")
	}
}

func TestBrowserCancellation(t *testing.T) {
	// Arrange.
	a := web.NewCapturePage()
	status, err := a.Discover(context.Background(), &environment.Local{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != action.Ready {
		t.Skip("Chromium not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	raw := json.RawMessage(`{"url":"http://127.0.0.1:1"}`)
	// Act.
	started := time.Now()
	_, err = a.Run(ctx, raw, grants)
	// Assert.
	if err == nil || time.Since(started) > 5*time.Second {
		t.Fatalf("browser did not stop promptly: %v, %s", err, time.Since(started))
	}
}

func TestBrowserRejectsOffHostnameNavigation(t *testing.T) {
	// Arrange.
	a := web.NewCapturePage()
	status, err := a.Discover(context.Background(), &environment.Local{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != action.Ready {
		t.Skip("Chromium not installed")
	}
	var outsideCalls atomic.Int32
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { outsideCalls.Add(1) }))
	defer outside.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(outside.URL, "127.0.0.1", "localhost", 1), 302)
	}))
	defer site.Close()
	raw, _ := json.Marshal(web.PageInput{URL: site.URL})
	// Act.
	_, err = a.Run(context.Background(), raw, grants)
	// Assert.
	if err == nil {
		t.Fatal("expected blocked off-hostname navigation")
	}
	if outsideCalls.Load() != 0 {
		t.Fatal("browser followed off-hostname redirect")
	}
}

func TestDiscoverySkipsNonHTMLAndDeduplicatesRedirects(t *testing.T) {
	// Arrange.
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<title>Home</title><a href="/about">About</a><a href="/alias">Alias</a><a href="/file">Download</a>`)
		case "/about":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<title>About</title>`)
		case "/alias":
			http.Redirect(w, r, "/about", 302)
		case "/file":
			w.Header().Set("Content-Type", "application/pdf")
			fmt.Fprint(w, "%PDF-example")
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()
	a := web.NewDiscoverPages()
	ready(t, a)
	// Act.
	result := run(t, a, web.DiscoverPagesInput{URL: site.URL, MaxPages: 10})
	out := decode[web.DiscoverPagesOutput](t, result)
	// Assert.
	if len(out.Pages) != 2 || out.SkippedNonHTMLPages != 1 || len(result.NextActions) != 10 {
		t.Fatalf("unexpected discovery: %+v, actions=%d", out, len(result.NextActions))
	}
	for _, path := range []string{"/file", "/missing"} {
		raw, _ := json.Marshal(web.DiscoverPagesInput{URL: site.URL + path, MaxPages: 2})
		if _, err := a.Run(context.Background(), raw, grants); err == nil {
			t.Errorf("accepted invalid review root %s", path)
		}
	}
}
