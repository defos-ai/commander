package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

const privateToken = "private-github-token-sentinel"
const githubReleases = "https://api.github.com/repos/defos-ai/example-agent/releases/"

func privateRelease(tag string) string {
	return fmt.Sprintf(`{"tag_name":%q,"draft":false,"prerelease":false,"published_at":"2026-10-01T12:00:00Z","assets":[
		{"id":11,"name":"agent.yaml","url":"https://untrusted.invalid/manifest"},
		{"id":12,"name":%q,"url":"https://untrusted.invalid/binary"}]}`, tag, binaryAsset)
}

func TestPrivatePackageDownloadsUseScopedAuthentication(t *testing.T) {
	for _, reference := range []string{packageReference, moduleReference} {
		t.Run(reference, func(t *testing.T) {
			ref, err := agentpackages.ParseReference(reference)
			if err != nil {
				t.Fatal(err)
			}
			const binary = "private executable fixture"
			manifestBody := packageManifest(binary)
			var authenticated, redirected, anonymous int
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				switch request.URL.Host {
				case "api.github.com":
					authenticated++
					if request.Header.Get("Authorization") != "Bearer "+privateToken || request.Method != http.MethodGet {
						t.Fatal("private GitHub API request lacks scoped bearer authentication")
					}
					switch request.URL.String() {
					case githubReleases + "tags/" + url.PathEscape(ref.ReleaseTag()):
						if request.Header.Get("Accept") != "application/vnd.github+json" {
							t.Fatal("release metadata must request JSON")
						}
						return response(request, http.StatusOK, privateRelease(ref.ReleaseTag())), nil
					case githubReleases + "assets/11", githubReleases + "assets/12":
						if request.Header.Get("Accept") != "application/octet-stream" {
							t.Fatal("release asset must request binary content")
						}
						result := response(request, http.StatusFound, "")
						result.Header.Set("Location", "https://release-assets.githubusercontent.com"+request.URL.Path+"?signature=private-signature")
						result.Header.Set("Set-Cookie", "private-session=sentinel")
						return result, nil
					}
				case "release-assets.githubusercontent.com":
					redirected++
					for _, header := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
						if request.Header.Get(header) != "" {
							t.Fatalf("redirect retained %s", header)
						}
					}
					if strings.HasSuffix(request.URL.Path, "/11") {
						return response(request, http.StatusOK, manifestBody), nil
					}
					return response(request, http.StatusOK, binary), nil
				case "github.com":
					anonymous++
					if request.Header.Get("Authorization") != "" {
						t.Fatal("private token leaked into a subsequent public request")
					}
					return response(request, http.StatusOK, manifestBody), nil
				}
				t.Fatal("download followed an unexpected URL from API metadata")
				return nil, errors.New("unexpected URL")
			}))
			t.Cleanup(resolver.CloseIdleConnections)
			token := []byte(privateToken)
			defer clear(token)
			ctx := packages.WithGitHubToken(t.Context(), token)
			pkg, err := resolver.Resolve(ctx, reference)
			if err != nil {
				t.Fatal(err)
			}
			path, err := resolver.DownloadNative(ctx, pkg, "linux", "amd64", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != binary {
				t.Fatal("private download did not produce the verified executable")
			}
			metadata, err := json.Marshal(pkg)
			if err != nil || strings.Contains(string(metadata), privateToken) {
				t.Fatal("package metadata retained authentication")
			}
			if _, err := resolver.Resolve(t.Context(), reference); err != nil {
				t.Fatal(err)
			}
			if authenticated != 4 || redirected != 2 || anonymous != 1 {
				t.Fatalf("unexpected request counts: API=%d, CDN=%d, public=%d", authenticated, redirected, anonymous)
			}
		})
	}
}

func TestPrivatePackageRejectsUnsafeMetadataAndRedirects(t *testing.T) {
	valid := privateRelease("v0.1.0")
	for _, test := range []struct {
		name, metadata, redirect string
	}{
		{name: "wrong tag", metadata: privateRelease("v0.2.0")},
		{name: "draft", metadata: strings.Replace(valid, `"draft":false`, `"draft":true`, 1)},
		{name: "prerelease", metadata: strings.Replace(valid, `"prerelease":false`, `"prerelease":true`, 1)},
		{name: "unpublished", metadata: strings.Replace(valid, `"2026-10-01T12:00:00Z"`, `null`, 1)},
		{name: "missing asset", metadata: strings.Replace(valid, `"agent.yaml"`, `"missing.yaml"`, 1)},
		{name: "duplicate asset", metadata: strings.Replace(valid, `"`+binaryAsset+`"`, `"agent.yaml"`, 1)},
		{name: "invalid asset ID", metadata: strings.Replace(valid, `"id":11`, `"id":0`, 1)},
		{name: "malformed metadata", metadata: privateToken},
		{name: "oversized metadata", metadata: strings.Repeat(" ", 2<<20) + valid},
		{name: "untrusted redirect", metadata: valid, redirect: "https://untrusted.invalid/?token=" + privateToken},
		{name: "insecure redirect", metadata: valid, redirect: "http://github.com/asset"},
		{name: "API redirect", metadata: valid, redirect: "https://api.github.com/user"},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					return response(request, http.StatusOK, test.metadata), nil
				}
				if test.redirect == "" || requests > 2 {
					t.Fatal("invalid metadata or redirect reached another endpoint")
				}
				result := response(request, http.StatusFound, "")
				result.Header.Set("Location", test.redirect)
				return result, nil
			}))
			t.Cleanup(resolver.CloseIdleConnections)
			ctx := packages.WithGitHubToken(t.Context(), []byte(privateToken))
			if _, err := resolver.Resolve(ctx, packageReference); err == nil || strings.Contains(err.Error(), privateToken) {
				t.Fatal("unsafe package was accepted or its response leaked into diagnostics")
			}
		})
	}
}

func TestPrivatePackageErrorsKeepCredentialsPrivate(t *testing.T) {
	for _, failure := range []string{"unauthorized", "forbidden", "missing", "transport", "manifest", "canceled", "invalid token"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				switch failure {
				case "invalid token":
					t.Fatal("invalid token reached transport")
				case "transport":
					return nil, errors.New(privateToken)
				case "canceled":
					cancel()
					return nil, request.Context().Err()
				case "manifest":
					if strings.Contains(request.URL.Path, "/tags/") {
						return response(request, http.StatusOK, privateRelease("v0.1.0")), nil
					}
					return response(request, http.StatusOK, privateToken+": value"), nil
				}
				status := map[string]int{"unauthorized": 401, "forbidden": 403, "missing": 404}[failure]
				return response(request, status, privateToken), nil
			}))
			t.Cleanup(resolver.CloseIdleConnections)
			token := []byte(privateToken)
			if failure == "invalid token" {
				token = append(token, '\n')
			}
			defer clear(token)
			_, err := resolver.Resolve(packages.WithGitHubToken(ctx, token), packageReference)
			if err == nil || strings.Contains(err.Error(), privateToken) {
				t.Fatal("error leaked credentials or request unexpectedly succeeded")
			}
			if failure == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation was lost")
			}
		})
	}
}
