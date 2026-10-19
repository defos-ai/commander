package packages_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/framework/agent/manifest"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

const (
	packageReference = "github.com/defos-ai/example-agent@v0.1.0"
	releaseURL       = "https://github.com/defos-ai/example-agent/releases/download/v0.1.0/"
	moduleReference  = "github.com/defos-ai/example-agent/apps/agents/example@v0.1.0"
	moduleReleaseURL = "https://github.com/defos-ai/example-agent/releases/download/apps%2Fagents%2Fexample%2Fv0.1.0/"
	binaryAsset      = "example-agent_linux_amd64"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func packageManifest(binary string) string {
	checksum := sha256.Sum256([]byte(binary))
	return fmt.Sprintf("schema_version: 1\ntype: example-agent\nversion: v0.1.0\nnative:\n  linux/amd64:\n    asset: %s\n    sha256: %q\n", binaryAsset, hex.EncodeToString(checksum[:]))
}

func TestParsePackageReference(t *testing.T) {
	for _, modulePath := range []string{"", "apps/agents/example", "agents/example_agent.v2"} {
		input := "github.com/defos-ai/example-agent"
		if modulePath != "" {
			input += "/" + modulePath
		}
		input += "@v0.1.0"
		ref, err := agentpackages.ParseReference(input)
		if err != nil {
			t.Fatal(err)
		}
		if ref.Owner != "defos-ai" || ref.Repository != "example-agent" || ref.ModulePath != modulePath || ref.Version != "v0.1.0" || ref.String() != input {
			t.Fatalf("unexpected reference: %#v (%q)", ref, ref.String())
		}
	}
	for _, input := range []string{
		"", "https://" + packageReference, "github.com/defos-ai/example-agent",
		"github.com/defos-ai/example-agent@stable", "github.com/defos-ai/example-agent@latest",
		"github.com/defos-ai/example-agent@v1.2", "github.com/defos-ai/example-agent@v01.2.3",
		"github.com/defos-ai/example-agent@v1.2.3-rc.1", "github.com/defos-ai/example-agent@v1.2.3+build",
		"github.com/../example-agent@v1.2.3", "github.com/defos-ai/../example-agent@v1.2.3",
		"github.com/defos-ai/example-agent@v1.2.3?token=abc", "github.com/defos-ai/example-agent@v1.2.3#fragment",
		"github.com/defos-ai/%2e%2e@v1.2.3", "github.com:443/defos-ai/example-agent@v1.2.3",
		"github.com/defos-ai/example-agent/@v1.2.3", "github.com/defos-ai/example-agent//agent@v1.2.3",
		"github.com/defos-ai/example-agent/apps//agent@v1.2.3", "github.com/defos-ai/example-agent/apps/@v1.2.3",
		"github.com/defos-ai/example-agent/../agent@v1.2.3", "github.com/defos-ai/example-agent/apps/./agent@v1.2.3",
		"github.com/defos-ai/example-agent/apps/%2e%2e@v1.2.3", "github.com/defos-ai/example-agent/apps%2Fagent@v1.2.3",
		"github.com/defos-ai/example-agent/apps/agent?token=abc@v1.2.3", "github.com/defos-ai/example-agent/apps/agent#fragment@v1.2.3",
		"github.com/defos-ai/example-agent/apps/agent@v1.2.3#selector", "github.com/defos-ai/example-agent/apps/agent@v1.2.3@v1.2.4",
		"github.com/defos-ai/example-agent/apps\\agent@v1.2.3", "github.com/defos-ai/example-agent/apps/agent..name@v1.2.3",
		"github.com/defos-ai/example-agent/apps/agent.@v1.2.3", "github.com/defos-ai/example-agent/apps/agent.lock@v1.2.3",
		"github.com/defos-ai/example-agent/apps/.agent@v1.2.3", "github.com/defos-ai/example-agent/apps/agent name@v1.2.3",
		" " + packageReference, packageReference + " ",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := agentpackages.ParseReference(input); err == nil {
				t.Fatalf("accepted invalid package reference %q", input)
			}
			resolver := packages.NewResolver(transportFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid reference reached transport")
				return nil, errors.New("unexpected request")
			}))
			if _, err := resolver.Resolve(context.Background(), input); err == nil {
				t.Fatal("resolver accepted invalid reference")
			}
		})
	}
}

func TestResolvePackage(t *testing.T) {
	for _, tc := range []struct{ reference, release string }{
		{packageReference, releaseURL},
		{moduleReference, moduleReleaseURL},
	} {
		t.Run(tc.reference, func(t *testing.T) {
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.String() != tc.release+"agent.yaml" {
					t.Fatalf("unexpected manifest request: %s %s", request.Method, request.URL)
				}
				return response(request, http.StatusOK, packageManifest("executable bytes")), nil
			}))
			pkg, err := resolver.Resolve(context.Background(), tc.reference)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Reference.String() != tc.reference || pkg.Manifest.Type != "example-agent" || pkg.Manifest.Version != "v0.1.0" || pkg.Manifest.Native["linux/amd64"].Asset != binaryAsset {
				t.Fatalf("unexpected package: %#v", pkg)
			}
		})
	}
}

func TestStableSelectionPinsNativeAndDockerPackages(t *testing.T) {
	for _, reference := range []string{packageReference, moduleReference} {
		t.Run(reference, func(t *testing.T) {
			const binary = "native fixture bytes"
			var requests []string
			transport := transportFunc(func(request *http.Request) (*http.Response, error) {
				requests = append(requests, request.URL.String())
				switch request.URL.String() {
				case "https://api.github.com/repos/defos-ai/example-agent/releases/latest":
					return response(request, http.StatusOK, `{"tag_name":"v0.1.0","draft":false,"prerelease":false}`), nil
				case releaseURL + "agent.yaml", moduleReleaseURL + "agent.yaml":
					body := packageManifest(binary) + "docker:\n  image: ghcr.io/defos-ai/example-agent@sha256:" + strings.Repeat("a", 64) + "\n"
					return response(request, http.StatusOK, body), nil
				case releaseURL + binaryAsset, moduleReleaseURL + binaryAsset:
					return response(request, http.StatusOK, binary), nil
				default:
					t.Fatalf("unexpected request: %s", request.URL)
					return nil, errors.New("unexpected request")
				}
			})
			metadata := agentpackages.NewClient(transport)
			t.Cleanup(metadata.CloseIdleConnections)
			pinned, err := metadata.Resolve(context.Background(), strings.TrimSuffix(reference, "@v0.1.0")+"@stable")
			if err != nil || pinned.String() != reference {
				t.Fatalf("stable reference: %s, %v", pinned.String(), err)
			}
			resolver := packages.NewResolver(transport)
			t.Cleanup(resolver.CloseIdleConnections)
			pkg, err := resolver.Resolve(context.Background(), pinned.String())
			if err != nil {
				t.Fatal(err)
			}
			image, err := pkg.Manifest.DockerImage()
			if err != nil || image != "ghcr.io/defos-ai/example-agent@sha256:"+strings.Repeat("a", 64) {
				t.Fatalf("Docker image: %s, %v", image, err)
			}
			path, err := resolver.DownloadNative(context.Background(), pkg, "linux", "amd64", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil || string(contents) != binary {
				t.Fatalf("native binary: %q, %v", contents, err)
			}
			base := releaseURL
			if pinned.ModulePath != "" {
				base = moduleReleaseURL
			}
			if len(requests) != 3 || requests[1] != base+"agent.yaml" || requests[2] != base+binaryAsset {
				t.Fatalf("downloads must stay on the selected release: %v", requests)
			}
		})
	}
}

func TestResolveRejectsBadResponses(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"missing release":   {http.StatusNotFound, "not found"},
		"partial manifest":  {http.StatusPartialContent, packageManifest("bytes")},
		"wrong version":     {http.StatusOK, strings.Replace(packageManifest("bytes"), "version: v0.1.0", "version: v0.2.0", 1)},
		"oversize manifest": {http.StatusOK, packageManifest("bytes") + "#" + strings.Repeat("x", 64*1024)},
	} {
		t.Run(name, func(t *testing.T) {
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, tc.status, tc.body), nil
			}))
			if _, err := resolver.Resolve(context.Background(), packageReference); err == nil {
				t.Fatal("accepted invalid release response")
			}
		})
	}
}

func TestResolvePermitsGitHubAssetRedirects(t *testing.T) {
	for _, host := range []string{"github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"} {
		t.Run(host, func(t *testing.T) {
			requests := 0
			target := "https://" + host + "/release/asset?signature=example"
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
					t.Fatal("asset request contains credentials")
				}
				if requests == 1 {
					result := response(request, http.StatusFound, "")
					result.Header.Set("Location", target)
					result.Header.Set("Set-Cookie", "session=private; Path=/")
					return result, nil
				}
				if requests != 2 || request.URL.String() != target {
					t.Fatalf("unexpected redirect request: %s", request.URL)
				}
				return response(request, http.StatusOK, packageManifest("bytes")), nil
			}))
			if _, err := resolver.Resolve(context.Background(), packageReference); err != nil {
				t.Fatal(err)
			}
			if requests != 2 {
				t.Fatalf("expected redirect to be followed, got %d requests", requests)
			}
		})
	}
}

func TestResolveRejectsUnsafeRedirects(t *testing.T) {
	for _, target := range []string{
		"http://github.com/release/asset", "https://169.254.169.254/latest/meta-data/",
		"https://localhost/release/asset", "https://example.com/release/asset",
		"https://github.com.example.com/release/asset", "https://evilgithub.com/release/asset",
		"https://username:password@github.com/release/asset", "https://github.com:8443/release/asset",
	} {
		t.Run(target, func(t *testing.T) {
			requests := 0
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if requests > 1 {
					t.Fatal("unsafe redirect reached transport")
				}
				result := response(request, http.StatusFound, "")
				result.Header.Set("Location", target)
				return result, nil
			}))
			if _, err := resolver.Resolve(context.Background(), packageReference); err == nil {
				t.Fatal("accepted unsafe redirect")
			}
		})
	}
}

func TestResolveBoundsRedirectLoops(t *testing.T) {
	requests := 0
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests > 6 {
			t.Fatal("more than five redirects were followed")
		}
		result := response(request, http.StatusTemporaryRedirect, "")
		result.Header.Set("Location", releaseURL+"agent.yaml")
		return result, nil
	}))
	if _, err := resolver.Resolve(context.Background(), packageReference); err == nil {
		t.Fatal("accepted redirect loop")
	}
}

func TestResolveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	if _, err := resolver.Resolve(ctx, packageReference); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestResolveDoesNotExposeDownloadCredentials(t *testing.T) {
	const secret = "private-signed-download-token"
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.RawQuery == "" {
			result := response(request, http.StatusFound, "")
			result.Header.Set("Location", "https://release-assets.githubusercontent.com/asset?token="+secret)
			return result, nil
		}
		return nil, fmt.Errorf("failed request to %s", request.URL)
	}))
	_, err := resolver.Resolve(context.Background(), packageReference)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("expected sanitized download failure, got %v", err)
	}
}

func TestDownloadNativeVerifiesBeforeExecution(t *testing.T) {
	directory := t.TempDir()
	const binary = "executable bytes, not actually executed by these tests"
	downloads := 0
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case moduleReleaseURL + "agent.yaml":
			return response(request, http.StatusOK, packageManifest(binary)), nil
		case moduleReleaseURL + binaryAsset:
			downloads++
			if downloads > 1 {
				return response(request, http.StatusOK, binary), nil
			}
			result := response(request, http.StatusOK, "")
			result.Body = io.NopCloser(&observedReader{reader: strings.NewReader(binary), observe: func() {
				assertIncompleteFilesNotExecutable(t, directory)
				matches, err := filepath.Glob(filepath.Join(directory, "*", binaryAsset))
				if err != nil || len(matches) != 0 {
					t.Fatalf("unverified download has its final filename: %v, %v", matches, err)
				}
			}})
			return result, nil
		default:
			t.Fatalf("unexpected asset request: %s", request.URL)
			return nil, errors.New("unexpected request")
		}
	}))
	pkg, err := resolver.Resolve(context.Background(), moduleReference)
	if err != nil {
		t.Fatal(err)
	}
	path, err := resolver.DownloadNative(context.Background(), pkg, "linux", "amd64", directory)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) == directory || filepath.Dir(filepath.Dir(path)) != directory {
		t.Fatalf("download is not in a private child directory: %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != binary {
		t.Fatalf("downloaded bytes: %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		for _, target := range []string{path, filepath.Dir(path)} {
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("expected private executable/directory permissions on %s: %v, %v", target, info, err)
			}
		}
	}
	second, err := resolver.DownloadNative(context.Background(), pkg, "linux", "amd64", directory)
	if err != nil {
		t.Fatal(err)
	}
	if path == second {
		t.Fatal("a second download overwrote an existing executable")
	}
}

type observedReader struct {
	reader  io.Reader
	observe func()
}

func (r *observedReader) Read(data []byte) (int, error) {
	r.observe()
	if len(data) > 4 {
		data = data[:4]
	}
	return r.reader.Read(data)
}

func assertIncompleteFilesNotExecutable(t *testing.T, directory string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0111 != 0 {
			t.Errorf("unverified download is executable: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadNativeFailurePreservesExistingFiles(t *testing.T) {
	for _, failure := range []string{"checksum", "empty", "interrupted", "missing", "oversize", "cancelled", "unsafe redirect"} {
		t.Run(failure, func(t *testing.T) {
			directory := t.TempDir()
			sentinel := filepath.Join(directory, "existing-agent")
			if err := os.WriteFile(sentinel, []byte("retain me"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == releaseURL+"agent.yaml" {
					if failure == "empty" {
						return response(request, http.StatusOK, packageManifest("")), nil
					}
					return response(request, http.StatusOK, packageManifest("expected executable")), nil
				}
				if request.URL.String() != releaseURL+binaryAsset {
					t.Fatalf("unsafe asset request: %s", request.URL)
				}
				result := response(request, http.StatusOK, "corrupted executable")
				switch failure {
				case "empty":
					result.Body = io.NopCloser(strings.NewReader(""))
				case "interrupted":
					result.Body = io.NopCloser(io.MultiReader(strings.NewReader("partial"), failingReader{}))
				case "missing":
					result.StatusCode = http.StatusNotFound
				case "oversize":
					result.ContentLength = packages.MaxDownloadBytes + 1
					result.Body = io.NopCloser(&observedReader{reader: strings.NewReader(""), observe: func() {
						t.Error("read binary advertised above size limit")
					}})
				case "cancelled":
					cancel()
					return nil, request.Context().Err()
				case "unsafe redirect":
					result.StatusCode = http.StatusFound
					result.Header.Set("Location", "https://169.254.169.254/latest/meta-data/")
				}
				return result, nil
			}))
			pkg, err := resolver.Resolve(context.Background(), packageReference)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := resolver.DownloadNative(ctx, pkg, "linux", "amd64", directory); err == nil {
				t.Fatal("accepted failed download")
			} else if failure == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			entries, err := os.ReadDir(directory)
			if err != nil || len(entries) != 1 || entries[0].Name() != "existing-agent" {
				t.Fatalf("failed download left files or removed existing data: %v, %v", entries, err)
			}
			contents, err := os.ReadFile(sentinel)
			if err != nil || string(contents) != "retain me" {
				t.Fatalf("existing data changed: %q, %v", contents, err)
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestDownloadNativeRejectsUnsupportedAndForgedPackages(t *testing.T) {
	resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != releaseURL+"agent.yaml" {
			t.Fatalf("invalid package reached asset transport: %s", request.URL)
		}
		return response(request, http.StatusOK, packageManifest("bytes")), nil
	}))
	pkg, err := resolver.Resolve(context.Background(), packageReference)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []agentpackages.Reference{
		{Owner: "defos-ai", Repository: "example-agent", ModulePath: "apps/../agent", Version: "v0.1.0"},
		{Owner: "defos-ai", Repository: "example-agent/apps", ModulePath: "agent", Version: "v0.1.0"},
		{Owner: "defos-ai", Repository: "example-agent", ModulePath: "apps%2Fagent", Version: "v0.1.0"},
	} {
		forged := pkg
		forged.Reference = ref
		if _, err := resolver.DownloadNative(context.Background(), forged, "linux", "amd64", t.TempDir()); err == nil {
			t.Fatalf("accepted forged reference: %+v", ref)
		}
	}
	if _, err := resolver.DownloadNative(context.Background(), pkg, "darwin", "arm64", t.TempDir()); err == nil {
		t.Fatal("accepted unavailable platform")
	}
	asset := pkg.Manifest.Native["linux/amd64"]
	asset.Asset = "../../outside"
	pkg.Manifest.Native["linux/amd64"] = asset
	if _, err := resolver.DownloadNative(context.Background(), pkg, "linux", "amd64", t.TempDir()); err == nil {
		t.Fatal("accepted forged traversal asset")
	}
	pkg.Manifest.Native = nil
	pkg.Manifest.Docker = &manifest.Docker{Image: "ghcr.io/defos-ai/example-agent@sha256:" + strings.Repeat("a", 64)}
	if _, err := resolver.DownloadNative(context.Background(), pkg, "linux", "amd64", t.TempDir()); err == nil {
		t.Fatal("accepted Docker-only package for native download")
	}
}
