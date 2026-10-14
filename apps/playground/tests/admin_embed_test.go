//go:build embed_ui

package playground_test

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/playground/internal/local"
)

func TestAllInOneBundledAdmin(t *testing.T) {
	allInOneEnvironment(t)
	t.Chdir(t.TempDir())
	if err := os.Unsetenv("UI_STATIC_DIR"); err != nil {
		t.Fatal(err)
	}
	session, stop := startLocalAdminSession(t, local.Options{})
	index, _ := fetchLocalAsset(t, session.AdminURL, "/")
	if !bytes.Contains(index, []byte("COMMANDER | Administration")) {
		t.Fatal("bundled Admin was not served from an unrelated working directory")
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^" ]+\.(?:js|css))"`).FindAllSubmatch(index, -1)
	var javascript, stylesheet bool
	for _, match := range assets {
		path := string(match[1])
		_, headers := fetchLocalAsset(t, session.AdminURL, path)
		if headers.Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatal("bundled Admin asset lost immutable caching")
		}
		if strings.HasSuffix(path, ".js") {
			javascript = strings.Contains(headers.Get("Content-Type"), "javascript")
		}
		if strings.HasSuffix(path, ".css") {
			stylesheet = strings.Contains(headers.Get("Content-Type"), "text/css")
		}
	}
	if !javascript || !stylesheet {
		t.Fatalf("bundled Admin assets incomplete: javascript=%v stylesheet=%v", javascript, stylesheet)
	}
	stop()
	assertLocalHostStopped(t, session.AdminURL)
}
