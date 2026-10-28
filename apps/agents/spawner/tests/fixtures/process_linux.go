//go:build linux

package fixtures

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
)

func ProcessResolver(binary []byte, asset string) *packages.Resolver {
	checksum := sha256.Sum256(binary)
	manifest := fmt.Sprintf("schema_version: 1\ntype: fixture-agent\nversion: v0.1.0\nnative:\n  %s/%s:\n    asset: %s\n    sha256: %q\n", runtime.GOOS, runtime.GOARCH, asset, fmt.Sprintf("%x", checksum))
	return packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		if filepath.Base(request.URL.Path) == "agent.yaml" {
			return Response(request, http.StatusOK, manifest), nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(bytes.NewReader(binary)), Request: request}, nil
	}))
}

func ProcessRunning(t *testing.T, pid int) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data[strings.LastIndexByte(string(data), ')')+1:]))
	if len(fields) == 0 {
		t.Fatal("cannot inspect fixture process state")
	}
	return fields[0] != "Z" && fields[0] != "X"
}
