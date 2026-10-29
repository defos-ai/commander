package fakes

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
)

type PackageSource struct {
	binary            []byte
	releaseURL, image string
	latest            atomic.Int32
	latestRequests    atomic.Int32
	requests          atomic.Int32
	failDownload      atomic.Bool
}

func NewPackageSource(binary []byte, releaseURL, image string) *PackageSource {
	source := &PackageSource{binary: bytes.Clone(binary), releaseURL: releaseURL, image: image}
	source.latest.Store(1)
	return source
}
func (source *PackageSource) RoundTrip(request *http.Request) (*http.Response, error) {
	source.requests.Add(1)
	if request.URL.String() == "https://api.github.com/repos/defos-ai/fixture-agent/releases/latest" {
		source.latestRequests.Add(1)
		return source.response(request, http.StatusOK, fmt.Sprintf(`{"tag_name":"v0.%d.0","draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z"}`, source.latest.Load())), nil
	}
	asset := "fixture-agent"
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	checksum := sha256.Sum256(source.binary)
	for _, minor := range []int{1, 2} {
		version := fmt.Sprintf("v0.%d.0", minor)
		base := strings.Replace(source.releaseURL, "v0.1.0", version, 1)
		switch request.URL.String() {
		case base + "agent.yaml":
			manifest := fmt.Sprintf("schema_version: 1\ntype: fixture-agent\nversion: %s\ndocker:\n  image: %s\nnative:\n  %s/%s:\n    asset: %s\n    sha256: %q\n", version, source.image, runtime.GOOS, runtime.GOARCH, asset, fmt.Sprintf("%x", checksum))
			return source.response(request, http.StatusOK, manifest), nil
		case base + asset:
			if source.failDownload.Load() {
				return source.response(request, http.StatusServiceUnavailable, "try again"), nil
			}
			return source.response(request, http.StatusOK, string(source.binary)), nil
		}
	}
	return source.response(request, http.StatusNotFound, "not found"), nil
}
func (*PackageSource) response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
func (source *PackageSource) SetLatest(version int32)      { source.latest.Store(version) }
func (source *PackageSource) SetDownloadFailure(fail bool) { source.failDownload.Store(fail) }
func (source *PackageSource) RequestCount() int32          { return source.requests.Load() }
func (source *PackageSource) LatestRequestCount() int32    { return source.latestRequests.Load() }
