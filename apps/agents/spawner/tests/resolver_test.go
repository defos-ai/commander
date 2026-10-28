package packages_test

import (
	"context"
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
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/manifest"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

func TestResolveRejectsInvalidReferenceBeforeRequest(t *testing.T) {
	resolver := packages.NewResolver(fakes.Transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid reference reached transport")
		return nil, errors.New("unexpected request")
	}))
	for _, input := range []string{
		"github.com/defos-ai/example-agent@stable",
		"github.com/defos-ai/example-agent/../agent@v1.2.3",
	} {
		if _, err := resolver.Resolve(t.Context(), input); err == nil {
			t.Fatalf("resolver accepted invalid exact reference %q", input)
		}
	}
}

func TestResolvePackage(t *testing.T) {
	for _, tc := range []struct{ reference, release string }{
		{fixtures.PackageReference, fixtures.ReleaseURL},
		{fixtures.ModuleReference, fixtures.ModuleReleaseURL},
	} {
		t.Run(tc.reference, func(t *testing.T) {
			resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.String() != tc.release+"agent.yaml" {
					t.Fatalf("unexpected manifest request: %s %s", request.Method, request.URL)
				}
				return fixtures.Response(request, http.StatusOK, fixtures.PackageManifest("executable bytes")), nil
			}))
			pkg, err := resolver.Resolve(context.Background(), tc.reference)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.Reference.String() != tc.reference || pkg.Manifest.Type != "example-agent" || pkg.Manifest.Version != "v0.1.0" || pkg.Manifest.Native["linux/amd64"].Asset != fixtures.BinaryAsset {
				t.Fatalf("unexpected package: %#v", pkg)
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
		"partial manifest":  {http.StatusPartialContent, fixtures.PackageManifest("bytes")},
		"wrong version":     {http.StatusOK, strings.Replace(fixtures.PackageManifest("bytes"), "version: v0.1.0", "version: v0.2.0", 1)},
		"oversize manifest": {http.StatusOK, fixtures.PackageManifest("bytes") + "#" + strings.Repeat("x", 64*1024)},
	} {
		t.Run(name, func(t *testing.T) {
			resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
				return fixtures.Response(request, tc.status, tc.body), nil
			}))
			if _, err := resolver.Resolve(context.Background(), fixtures.PackageReference); err == nil {
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
			resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
				requests++
				if request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
					t.Fatal("asset request contains credentials")
				}
				if requests == 1 {
					result := fixtures.Response(request, http.StatusFound, "")
					result.Header.Set("Location", target)
					result.Header.Set("Set-Cookie", "session=private; Path=/")
					return result, nil
				}
				if requests != 2 || request.URL.String() != target {
					t.Fatalf("unexpected redirect request: %s", request.URL)
				}
				return fixtures.Response(request, http.StatusOK, fixtures.PackageManifest("bytes")), nil
			}))
			if _, err := resolver.Resolve(context.Background(), fixtures.PackageReference); err != nil {
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
			resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
				requests++
				if requests > 1 {
					t.Fatal("unsafe redirect reached transport")
				}
				result := fixtures.Response(request, http.StatusFound, "")
				result.Header.Set("Location", target)
				return result, nil
			}))
			if _, err := resolver.Resolve(context.Background(), fixtures.PackageReference); err == nil {
				t.Fatal("accepted unsafe redirect")
			}
		})
	}
}

func TestResolveBoundsRedirectLoops(t *testing.T) {
	requests := 0
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests > 6 {
			t.Fatal("more than five redirects were followed")
		}
		result := fixtures.Response(request, http.StatusTemporaryRedirect, "")
		result.Header.Set("Location", fixtures.ReleaseURL+"agent.yaml")
		return result, nil
	}))
	if _, err := resolver.Resolve(context.Background(), fixtures.PackageReference); err == nil {
		t.Fatal("accepted redirect loop")
	}
}

func TestResolveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		cancel()
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	if _, err := resolver.Resolve(ctx, fixtures.PackageReference); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestResolveDoesNotExposeDownloadCredentials(t *testing.T) {
	const secret = "private-signed-download-token"
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		if request.URL.RawQuery == "" {
			result := fixtures.Response(request, http.StatusFound, "")
			result.Header.Set("Location", "https://release-assets.githubusercontent.com/asset?token="+secret)
			return result, nil
		}
		return nil, fmt.Errorf("failed request to %s", request.URL)
	}))
	_, err := resolver.Resolve(context.Background(), fixtures.PackageReference)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("expected sanitized download failure, got %v", err)
	}
}

func TestDownloadNativeVerifiesBeforeExecution(t *testing.T) {
	directory := t.TempDir()
	const binary = "executable bytes, not actually executed by these tests"
	downloads := 0
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case fixtures.ModuleReleaseURL + "agent.yaml":
			return fixtures.Response(request, http.StatusOK, fixtures.PackageManifest(binary)), nil
		case fixtures.ModuleReleaseURL + fixtures.BinaryAsset:
			downloads++
			if downloads > 1 {
				return fixtures.Response(request, http.StatusOK, binary), nil
			}
			result := fixtures.Response(request, http.StatusOK, "")
			result.Body = io.NopCloser(fakes.NewObservedReader(strings.NewReader(binary), func() {
				assertIncompleteFilesNotExecutable(t, directory)
				matches, err := filepath.Glob(filepath.Join(directory, "*", fixtures.BinaryAsset))
				if err != nil || len(matches) != 0 {
					t.Fatalf("unverified download has its final filename: %v, %v", matches, err)
				}
			}))
			return result, nil
		default:
			t.Fatalf("unexpected asset request: %s", request.URL)
			return nil, errors.New("unexpected request")
		}
	}))
	pkg, err := resolver.Resolve(context.Background(), fixtures.ModuleReference)
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
			resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == fixtures.ReleaseURL+"agent.yaml" {
					if failure == "empty" {
						return fixtures.Response(request, http.StatusOK, fixtures.PackageManifest("")), nil
					}
					return fixtures.Response(request, http.StatusOK, fixtures.PackageManifest("expected executable")), nil
				}
				if request.URL.String() != fixtures.ReleaseURL+fixtures.BinaryAsset {
					t.Fatalf("unsafe asset request: %s", request.URL)
				}
				result := fixtures.Response(request, http.StatusOK, "corrupted executable")
				switch failure {
				case "empty":
					result.Body = io.NopCloser(strings.NewReader(""))
				case "interrupted":
					result.Body = io.NopCloser(io.MultiReader(strings.NewReader("partial"), fakes.FailingReader{}))
				case "missing":
					result.StatusCode = http.StatusNotFound
				case "oversize":
					result.ContentLength = packages.MaxDownloadBytes + 1
					result.Body = io.NopCloser(fakes.NewObservedReader(strings.NewReader(""), func() {
						t.Error("read binary advertised above size limit")
					}))
				case "cancelled":
					cancel()
					return nil, request.Context().Err()
				case "unsafe redirect":
					result.StatusCode = http.StatusFound
					result.Header.Set("Location", "https://169.254.169.254/latest/meta-data/")
				}
				return result, nil
			}))
			pkg, err := resolver.Resolve(context.Background(), fixtures.PackageReference)
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

func TestDownloadNativeRejectsUnsupportedAndForgedPackages(t *testing.T) {
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != fixtures.ReleaseURL+"agent.yaml" {
			t.Fatalf("invalid package reached asset transport: %s", request.URL)
		}
		return fixtures.Response(request, http.StatusOK, fixtures.PackageManifest("bytes")), nil
	}))
	pkg, err := resolver.Resolve(context.Background(), fixtures.PackageReference)
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
