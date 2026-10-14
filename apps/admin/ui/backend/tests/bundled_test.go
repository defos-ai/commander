//go:build embed_ui

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/admin/ui/backend/cli"
)

func TestBundledFrontend(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"UI_STATIC_DIR", "UI_API_UPSTREAMS", "UI_PUBLIC_ORIGIN"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("runs without a frontend directory", func(t *testing.T) {
		baseURL, stop := startBundledUI(t)
		index, _ := fetchBundledAsset(t, baseURL, "/")
		if !bytes.Contains(index, []byte("COMMANDER | Administration")) {
			t.Fatal("bundled index was not served")
		}
		deepLink, _ := fetchBundledAsset(t, baseURL, "/connections/local/operations/example")
		if !bytes.Equal(index, deepLink) {
			t.Fatal("bundled SPA deep link did not return index.html")
		}
		health, _ := fetchBundledAsset(t, baseURL, "/healthz")
		if string(health) != "ok\n" {
			t.Fatal("bundled server health check failed")
		}
		assets := regexp.MustCompile(`(?:src|href)="(/assets/[^" ]+\.(?:js|css))"`).FindAllSubmatch(index, -1)
		var javascript, stylesheet, font bool
		for _, match := range assets {
			assetPath := string(match[1])
			body, headers := fetchBundledAsset(t, baseURL, assetPath)
			if headers.Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatal("bundled hashed asset lost immutable caching")
			}
			switch {
			case strings.HasSuffix(assetPath, ".js"):
				javascript = strings.Contains(headers.Get("Content-Type"), "javascript")
			case strings.HasSuffix(assetPath, ".css"):
				stylesheet = strings.Contains(headers.Get("Content-Type"), "text/css")
				fontPaths := regexp.MustCompile(`url\(["']?([^"')]+\.(?:woff2?|otf|ttf))["']?\)`).FindAllSubmatch(body, -1)
				cssURL, _ := url.Parse(baseURL + assetPath)
				for _, fontMatch := range fontPaths {
					fontURL, err := cssURL.Parse(string(fontMatch[1]))
					if err != nil || fontURL.Host != cssURL.Host || !strings.HasPrefix(fontURL.Path, "/assets/") {
						t.Fatal("bundled font does not reference a local packaged asset")
					}
					_, fontHeaders := fetchBundledAsset(t, baseURL, fontURL.Path)
					if strings.Contains(fontHeaders.Get("Content-Type"), "text/html") {
						t.Fatal("font request returned the SPA index")
					}
					font = true
				}
			}
		}
		if !javascript || !stylesheet || !font {
			t.Fatalf("bundled asset references incomplete: javascript=%v stylesheet=%v font=%v", javascript, stylesheet, font)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/ui/connections", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Commander-UI", "1")
		response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Items []json.RawMessage `json:"items"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || result.Items == nil || len(result.Items) != 0 {
			t.Fatalf("UI routes were replaced by static content: status=%d error=%v", response.StatusCode, err)
		}
		stop()
	})
	for _, useFlag := range []bool{false, true} {
		name := "environment overrides bundle"
		if useFlag {
			name = "flag overrides environment and bundle"
		}
		t.Run(name, func(t *testing.T) {
			directory := builtFrontend(t)
			t.Setenv("UI_STATIC_DIR", directory)
			var args []string
			if useFlag {
				t.Setenv("UI_STATIC_DIR", "missing-frontend")
				args = []string{"--static-dir=" + directory}
			}
			baseURL, stop := startBundledUI(t, args...)
			body, _ := fetchBundledAsset(t, baseURL, "/")
			if string(body) != "<html>Commander</html>" {
				t.Fatal("explicit static directory did not override bundled assets")
			}
			stop()
		})
	}
	t.Run("blank environment does not select bundle", func(t *testing.T) {
		t.Setenv("UI_STATIC_DIR", "")
		err := cli.Run(t.Context(), []string{"--listen-address=127.0.0.1:0"}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "static-dir") {
			t.Fatal("explicit blank static directory was silently replaced by bundled assets")
		}
	})
}

func startBundledUI(t *testing.T, args ...string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	ready := make(chan net.Addr, 1)
	done := make(chan struct{})
	var output bytes.Buffer
	var runErr error
	go func() {
		defer close(done)
		runErr = cli.RunWithOptions(ctx, append(args, "--listen-address=127.0.0.1:0"), &output, cli.Options{
			Ready: func(address net.Addr) { ready <- address },
		})
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Errorf("bundled UI stopped with error: %v", runErr)
			}
		case <-time.After(6 * time.Second):
			t.Error("bundled UI did not shut down")
		}
	}
	t.Cleanup(stop)
	select {
	case address := <-ready:
		return "http://" + address.String(), stop
	case <-done:
		t.Fatalf("bundled UI startup failed: %v", runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("bundled UI did not become ready")
	}
	return "", stop
}

func fetchBundledAsset(t *testing.T, baseURL, path string) ([]byte, http.Header) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || len(body) == 0 {
		t.Fatalf("fetch %s: status=%d error=%v", path, response.StatusCode, err)
	}
	if response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("fetch %s: missing security headers", path)
	}
	return body, response.Header
}
