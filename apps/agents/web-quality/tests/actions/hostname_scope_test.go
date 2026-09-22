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
	"commander/framework/agent/action"
	"commander/framework/agent/environment"
)

func TestWebsiteScopeFollowsHostnameAcrossSchemesAndPorts(t *testing.T) {
	// Arrange: an HTTP entry redirects to HTTPS; a second port hosts another page.
	var externalCalls atomic.Int32
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalCalls.Add(1)
	}))
	defer external.Close()
	otherHostname := strings.Replace(external.URL, "127.0.0.1", "localhost", 1)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<title>Second port</title>`)
	}))
	defer page.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<title>Secure home</title><a href="%s">Page</a><a href="%s">Other host</a><a href="http://child.127.0.0.1/">Subdomain</a>`, page.URL, otherHostname)
	}))
	defer secure.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secure.URL, http.StatusMovedPermanently)
	}))
	defer entry.Close()
	// Trust only the test server's certificate; production TLS verification remains enabled.
	previous := http.DefaultTransport
	http.DefaultTransport = secure.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	discover, links := web.NewDiscoverPages(), web.NewCheckLinks()
	ready(t, discover)
	ready(t, links)

	// Act.
	pages := decode[web.DiscoverPagesOutput](t, run(t, discover, web.DiscoverPagesInput{URL: entry.URL, MaxPages: 10}))
	checked := decode[web.CheckLinksOutput](t, run(t, links, web.PageInput{URL: entry.URL}))

	// Assert: redirects and links use hostname scope while preserving each page's URL.
	if len(pages.Pages) != 2 || pages.Pages[0].URL != secure.URL+"/" || pages.Pages[1].URL != page.URL+"/" || pages.Truncated || pages.SkippedExternalLinks != 2 {
		t.Fatalf("unexpected pages: %+v", pages)
	}
	if checked.CheckedLinks != 1 || checked.BrokenLinks != 0 || checked.SkippedExternalLinks != 2 || externalCalls.Load() != 0 {
		t.Fatalf("unexpected links: %+v; external requests=%d", checked, externalCalls.Load())
	}
}

func TestBrowserAllowsSameHostnamePortRedirect(t *testing.T) {
	// Arrange: browser navigation follows the same hostname rule as HTTP discovery.
	a := web.NewCapturePage()
	status, err := a.Discover(context.Background(), &environment.Local{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status != action.Ready {
		t.Skip("Chromium not installed")
	}
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<title>Another port</title><h1>Preview</h1>`)
	}))
	defer page.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, page.URL, http.StatusFound)
	}))
	defer entry.Close()
	raw, _ := json.Marshal(web.PageInput{URL: entry.URL})

	// Act.
	result, err := a.Run(t.Context(), raw, grants)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	out := decode[web.CapturePageOutput](t, result)
	if out.URL != page.URL+"/" || out.Title != "Another port" || len(result.Artifacts) != 2 {
		t.Fatalf("same-hostname redirect was not captured: %+v", out)
	}
}
