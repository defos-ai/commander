package packages_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	agentpackages "github.com/defos-ai/commander/framework/agent/packages"
)

func TestPinStableUsesScopedAuthenticationAndKeepsExactPins(t *testing.T) {
	for _, reference := range []string{packageReference, moduleReference} {
		t.Run(reference, func(t *testing.T) {
			requests := 0
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.String() != githubReleases+"latest" || request.Header.Get("Accept") != "application/vnd.github+json" {
					t.Fatalf("unexpected latest-release request: %s %s", request.Method, request.URL)
				}
				deadline, ok := request.Context().Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {
					t.Fatal("latest-release request must have a bounded deadline")
				}
				expectedAuth := ""
				if requests == 1 {
					expectedAuth = "Bearer " + privateToken
				}
				if request.Header.Get("Authorization") != expectedAuth || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
					t.Fatal("latest-release request did not isolate scoped authentication")
				}
				versions := []string{"v0.1.0", "v0.2.0", "v0.3.0"}
				if requests >= len(versions) {
					t.Fatal("an exact pin contacted the network")
				}
				body := privateRelease(versions[requests])
				requests++
				return response(request, http.StatusOK, body), nil
			}))
			t.Cleanup(resolver.CloseIdleConnections)
			selector := strings.TrimSuffix(reference, "@v0.1.0")
			first, err := resolver.Pin(t.Context(), "https://"+selector)
			if err != nil || first.String() != reference {
				t.Fatalf("implicit stable pin: %#v, %v", first, err)
			}
			token := []byte(privateToken)
			defer clear(token)
			private, err := resolver.Pin(packages.WithGitHubToken(t.Context(), token), selector+"@stable")
			if err != nil || private.Version != "v0.2.0" || private.ModulePath != first.ModulePath {
				t.Fatalf("private stable pin: %#v, %v", private, err)
			}
			for _, exact := range []string{first.String(), "https://" + first.String()} {
				pinned, err := resolver.Pin(t.Context(), exact)
				if err != nil || pinned != first || requests != 2 {
					t.Fatalf("saved exact pin changed or made a request: %#v, %v", pinned, err)
				}
			}
			public, err := resolver.Pin(t.Context(), selector)
			if err != nil || public.Version != "v0.3.0" || public.ModulePath != first.ModulePath || requests != 3 {
				t.Fatalf("fresh public selection: %#v, %v", public, err)
			}
		})
	}
}

func TestPinRejectsInvalidLatestWithoutFallbackOrSecretLeaks(t *testing.T) {
	valid := privateRelease("v0.1.0")
	for _, test := range []struct {
		name, body, redirect string
		status               int
		transportError       bool
	}{
		{name: "malformed JSON", body: privateToken},
		{name: "missing version", body: strings.Replace(valid, `"tag_name":"v0.1.0",`, "", 1)},
		{name: "missing draft", body: strings.Replace(valid, `"draft":false,`, "", 1)},
		{name: "missing prerelease", body: strings.Replace(valid, `"prerelease":false,`, "", 1)},
		{name: "draft", body: strings.Replace(valid, `"draft":false`, `"draft":true`, 1)},
		{name: "prerelease", body: strings.Replace(valid, `"prerelease":false`, `"prerelease":true`, 1)},
		{name: "unpublished", body: strings.Replace(valid, `"2026-10-01T12:00:00Z"`, "null", 1)},
		{name: "zero version", body: privateRelease("v0.0.0")},
		{name: "prerelease tag", body: privateRelease("v0.2.0-rc.1")},
		{name: "module tag", body: privateRelease("apps/agents/example/v0.1.0")},
		{name: "trailing JSON", body: valid + `{ "token": "` + privateToken + `" }`},
		{name: "oversized metadata", body: strings.Repeat(" ", 2<<20) + valid},
		{name: "unauthorized", status: http.StatusUnauthorized, body: privateToken},
		{name: "missing latest", status: http.StatusNotFound, body: privateToken},
		{name: "rate limited", status: http.StatusTooManyRequests, body: privateToken},
		{name: "transport", transportError: true},
		{name: "API redirect", redirect: githubReleases + "tags/v0.1.0"},
		{name: "asset host redirect", redirect: "https://github.com/defos-ai/example-agent/metadata"},
		{name: "CDN redirect", redirect: "https://release-assets.githubusercontent.com/metadata?token=" + privateToken},
		{name: "untrusted redirect", redirect: "https://untrusted.invalid/?token=" + privateToken},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
				requests++
				if requests > 1 || request.URL.String() != githubReleases+"latest" {
					t.Fatal("invalid latest metadata caused a redirect or release fallback")
				}
				if test.transportError {
					return nil, errors.New(privateToken)
				}
				status := test.status
				if status == 0 {
					status = http.StatusOK
				}
				result := response(request, status, test.body)
				if test.redirect != "" {
					result.StatusCode = http.StatusFound
					result.Header.Set("Location", test.redirect)
				}
				return result, nil
			}))
			t.Cleanup(resolver.CloseIdleConnections)
			ctx := packages.WithGitHubToken(t.Context(), []byte(privateToken))
			pin, err := resolver.Pin(ctx, strings.TrimSuffix(packageReference, "@v0.1.0"))
			if err == nil || pin != (agentpackages.Reference{}) || strings.Contains(err.Error(), privateToken) {
				t.Fatalf("invalid latest response accepted or leaked: %#v, %v", pin, err)
			}
		})
	}
}

func TestPinPreservesCancellation(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		if cancelBefore {
			cancel()
		}
		resolver := packages.NewResolver(transportFunc(func(request *http.Request) (*http.Response, error) {
			if cancelBefore {
				t.Fatal("already canceled resolution reached transport")
			}
			cancel()
			return response(request, http.StatusOK, privateRelease("v0.1.0")), nil
		}))
		t.Cleanup(resolver.CloseIdleConnections)
		if _, err := resolver.Pin(ctx, strings.TrimSuffix(packageReference, "@v0.1.0")); !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
		if _, err := resolver.Pin(ctx, packageReference); !errors.Is(err, context.Canceled) {
			t.Fatalf("exact pin ignored cancellation: %v", err)
		}
		cancel()
	}
}
