package tests

import (
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/defos-ai/commander/apps/admin/ui/backend/static"
	"github.com/defos-ai/commander/apps/admin/ui/backend/tests/fixtures"
)

func TestStaticFrontend(t *testing.T) {
	directory := fixtures.BuiltFrontend(t)
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("not frontend content"), 0600); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for name, content := range fixtures.FrontendFiles() {
		files[name] = &fstest.MapFile{Data: []byte(content), Mode: 0600}
	}
	files[".env"] = &fstest.MapFile{Data: []byte("not frontend content"), Mode: 0600}
	for _, source := range []struct {
		name string
		open func() (*static.Handler, error)
	}{
		{"directory", func() (*static.Handler, error) { return static.NewHandler(directory) }},
		{"fs", func() (*static.Handler, error) { return static.NewHandlerFS(files) }},
	} {
		t.Run(source.name, func(t *testing.T) {
			handler, err := source.open()
			if err != nil {
				t.Fatal(err)
			}
			defer handler.Close()
			checkStaticFrontend(t, handler)
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
		handler, err := static.NewHandler(directory)
		if err != nil {
			t.Fatal(err)
		}
		defer handler.Close()
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/escaped.txt", nil))

		if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "secret outside frontend") {
			t.Fatalf("symlink escaped frontend: %d %q", recorder.Code, recorder.Body.String())
		}
	})
}

func checkStaticFrontend(t *testing.T, handler http.Handler) {
	t.Helper()
	for _, test := range []struct {
		name, method, path, body, cache, contentType string
		byteRange, contentRange                      string
		status                                       int
	}{
		{name: "index", path: "/", body: "<html>Commander</html>", status: 200, cache: "no-cache", contentType: "text/html"},
		{name: "head index", method: http.MethodHead, path: "/", status: 200, cache: "no-cache", contentType: "text/html"},
		{name: "deep link", path: "/connections/local/operations/123", body: "<html>Commander</html>", status: 200, cache: "no-cache"},
		{name: "asset", path: "/assets/app-abcd1234.js", body: "console.log('Commander')", status: 200, cache: "public, max-age=31536000, immutable", contentType: "javascript"},
		{name: "stylesheet", path: "/assets/app-abcd1234.css", body: "body{color:black}", status: 200, cache: "public, max-age=31536000, immutable", contentType: "text/css"},
		{name: "font", path: "/assets/font-abcd1234.woff2", body: "font fixture", status: 200, cache: "public, max-age=31536000, immutable", contentType: "font/woff2"},
		{name: "head", method: http.MethodHead, path: "/assets/app-abcd1234.js", status: 200, cache: "public, max-age=31536000, immutable"},
		{name: "range", path: "/assets/app-abcd1234.js", byteRange: "bytes=0-6", contentRange: "bytes 0-6/24", body: "console", status: 206, cache: "public, max-age=31536000, immutable"},
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
			request := httptest.NewRequest(method, test.path, nil)
			if test.byteRange != "" {
				request.Header.Set("Range", test.byteRange)
			}

			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
			if test.status < 300 && recorder.Body.String() != test.body {
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
			if test.contentRange != "" && recorder.Header().Get("Content-Range") != test.contentRange {
				t.Errorf("range = %q, want %q", recorder.Header().Get("Content-Range"), test.contentRange)
			}
			if test.contentType != "" {
				mediaType, _, err := mime.ParseMediaType(recorder.Header().Get("Content-Type"))
				matches := mediaType == test.contentType
				if test.contentType == "javascript" {
					matches = mediaType == "application/javascript" || mediaType == "text/javascript"
				}
				if err != nil || !matches {
					t.Errorf("content type = %q, want %q", recorder.Header().Get("Content-Type"), test.contentType)
				}
			}
		})
	}
}

func TestStaticFSRequiresIndex(t *testing.T) {
	for name, files := range map[string]fstest.MapFS{
		"missing":   {},
		"directory": {"index.html": &fstest.MapFile{Mode: fs.ModeDir | 0700}},
	} {
		t.Run(name, func(t *testing.T) {
			handler, err := static.NewHandlerFS(files)
			if err == nil {
				handler.Close()
				t.Fatal("frontend without a regular index.html was accepted")
			}
		})
	}
}
