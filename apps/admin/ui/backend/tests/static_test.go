package tests

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/admin/ui/backend/static"
)

func TestStaticFrontend(t *testing.T) {
	directory := builtFrontend(t)
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("not frontend content"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := static.NewHandler(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	for _, test := range []struct {
		name, method, path, body, cache string
		status                          int
	}{
		{name: "index", path: "/", body: "<html>Commander</html>", status: 200, cache: "no-cache"},
		{name: "deep link", path: "/connections/local/operations/123", body: "<html>Commander</html>", status: 200, cache: "no-cache"},
		{name: "asset", path: "/assets/app-abcd1234.js", body: "console.log('Commander')", status: 200, cache: "public, max-age=31536000, immutable"},
		{name: "head", method: http.MethodHead, path: "/assets/app-abcd1234.js", status: 200, cache: "public, max-age=31536000, immutable"},
		{name: "health", path: "/healthz", body: "ok\n", status: 200, cache: "no-cache"},
		{name: "head health", method: http.MethodHead, path: "/healthz", status: 200, cache: "no-cache"},
		{name: "missing asset", path: "/assets/missing.js", status: 404, cache: "no-cache"},
		{name: "missing stylesheet", path: "/missing.css", status: 404, cache: "no-cache"},
		{name: "missing extensionless asset", path: "/assets/missing", status: 404, cache: "no-cache"},
		{name: "no directory listing", path: "/assets/", status: 404, cache: "no-cache"},
		{name: "hidden file", path: "/.env", status: 404, cache: "no-cache"},
		{name: "traversal", path: "/../secret.txt", status: 404, cache: "no-cache"},
		{name: "encoded traversal", path: "/%2e%2e/secret.txt", status: 404, cache: "no-cache"},
		{name: "backslash", path: "/..%5csecret.txt", status: 404, cache: "no-cache"},
		{name: "null", path: "/%00", status: 404, cache: "no-cache"},
		{name: "method", method: http.MethodPost, path: "/", status: 405, cache: "no-cache"},
	} {
		t.Run(test.name, func(t *testing.T) {
			method := test.method
			if method == "" {
				method = http.MethodGet
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(method, test.path, nil))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if test.status == 200 && recorder.Body.String() != test.body {
				t.Errorf("body = %q, want %q", recorder.Body.String(), test.body)
			}
			if recorder.Header().Get("Cache-Control") != test.cache {
				t.Errorf("cache = %q, want %q", recorder.Header().Get("Cache-Control"), test.cache)
			}
			if recorder.Header().Get("X-Content-Type-Options") != "nosniff" || recorder.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" {
				t.Error("missing browser security headers")
			}
			if test.status == 405 && recorder.Header().Get("Allow") != "GET, HEAD" {
				t.Error("missing allowed methods")
			}
		})
	}

	t.Run("symlinks cannot escape frontend", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("secret outside frontend"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(directory, "escaped.txt")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/escaped.txt", nil))
		if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "secret outside frontend") {
			t.Fatalf("symlink escaped frontend: %d %q", recorder.Code, recorder.Body.String())
		}
	})
}

func builtFrontend(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"index.html": "<html>Commander</html>", "assets/app-abcd1234.js": "console.log('Commander')",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}
