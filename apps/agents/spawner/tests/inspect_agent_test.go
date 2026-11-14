package packages_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	spawneractions "github.com/defos-ai/commander/apps/agents/spawner/actions"
	"github.com/defos-ai/commander/apps/agents/spawner/packages"
	"github.com/defos-ai/commander/apps/agents/spawner/runtimes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/spawner/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/domain/agents"
)

func TestInspectAgentPinsPublicMetadataWithoutInstallingOrApprovingSource(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "environment-token-sentinel")
	source := fakes.NewInspectionSource(fixtures.InspectionManifest(), "")
	resolver := fixtures.InspectionResolver(t, source)
	executor := fixtures.InspectAction(t, resolver, runtimes.RuntimeNative)
	ctx, requestID, journal := fixtures.SpawnCheckpoints(t)
	inheritedToken := []byte("inherited-token-sentinel")
	ctx = packages.WithGitHubToken(ctx, inheritedToken)
	questions := fakes.NewSourceQuestions()
	ctx = action.WithQuestions(ctx, questions)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	result, err := executor.Run(ctx, json.RawMessage(`{"package":"https://github.com/other/worker-agent"}`))

	if err != nil {
		t.Fatal(err)
	}
	var output spawneractions.InspectAgentOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	want := spawneractions.InspectAgentOutput{
		Package: "github.com/other/worker-agent@v1.2.3", AgentType: "worker-agent", Version: "v1.2.3",
		Runtime: runtimes.RuntimeNative, NativePlatform: runtime.GOOS + "/" + runtime.GOARCH,
		NativePlatforms: []string{"linux/amd64", "windows/arm64"}, DockerImage: fixtures.InspectionImage,
		Dependencies: []agents.Dependency{
			{Name: "lightpanda", Description: "Browser engine used for navigation.", Version: "v0.2.0"},
			{Name: "hermes", Description: "Browser automation CLI."},
		},
	}
	if !reflect.DeepEqual(output, want) {
		t.Fatalf("inspection lost package installation guidance: %+v", output)
	}
	wantRequests := []fakes.InspectionRequest{
		{URL: "https://api.github.com/repos/other/worker-agent/releases/latest", Accept: "application/vnd.github+json"},
		{URL: "https://github.com/other/worker-agent/releases/download/v1.2.3/agent.yaml"},
	}
	if !reflect.DeepEqual(source.Requests(), wantRequests) {
		t.Fatalf("public inspection must read only anonymous release metadata: %+v", source.Requests())
	}
	if len(journal.Checkpoints()) != 0 || result.Session != nil || len(result.Artifacts) != 0 || len(result.NextActions) != 0 {
		t.Fatal("inspection produced launch state, artifacts or follow-up actions")
	}
	select {
	case <-questions.Requests():
		t.Fatal("inspection asked for installation approval")
	default:
	}
	if string(inheritedToken) != "inherited-token-sentinel" {
		t.Fatal("public inspection modified caller-owned credentials")
	}

	pinned, err := runtimes.PinPackage(ctx, requestID, output.Package, resolver)

	if !errors.Is(err, packages.ErrSourceApprovalRequired) || pinned != "" {
		t.Fatalf("inspection must not approve a subsequent installation: %v", err)
	}
	if !reflect.DeepEqual(source.Requests(), wantRequests) || len(journal.Checkpoints()) != 0 {
		t.Fatal("unapproved installation fetched or persisted package selection")
	}
}

func TestInspectAgentUsesPrivateCredentialsOnlyForFixedMetadataEndpoints(t *testing.T) {
	for _, test := range []struct {
		name        string
		kind        runtimes.RuntimeKind
		environment bool
	}{
		{name: "environment takes precedence", kind: runtimes.RuntimeDocker, environment: true},
		{name: "managed secret when environment is absent", kind: runtimes.RuntimeKubernetes},
	} {
		t.Run(test.name, func(t *testing.T) {
			const token = "private-token-sentinel"
			t.Setenv("GITHUB_TOKEN", "")
			if test.environment {
				t.Setenv("GITHUB_TOKEN", token)
			}
			manifest := fixtures.InspectionDockerManifest()
			if test.environment {
				manifest = fixtures.InspectionManifest()
			}
			source := fakes.NewInspectionSource(manifest, token)
			resolver := fixtures.InspectionResolver(t, source)
			executor := fixtures.InspectAction(t, resolver, test.kind)
			secretRequests := 0
			ctx := action.WithSecrets(t.Context(), fakes.SecretRequester(func(_ context.Context, name string) ([]byte, error) {
				secretRequests++
				if name != "GITHUB_TOKEN" {
					return nil, errors.New("unexpected secret name")
				}
				return []byte(token), nil
			}))

			result, err := executor.Run(ctx, json.RawMessage(`{"package":"github.com/other/worker-agent@v1.2.3","visibility":"private"}`))

			if err != nil {
				t.Fatal(err)
			}
			var output spawneractions.InspectAgentOutput
			if err := json.Unmarshal(result.Output, &output); err != nil {
				t.Fatal(err)
			}
			if output.Runtime != test.kind || output.NativePlatform != "" || strings.Contains(string(result.Output), `"native_platform":`) {
				t.Fatalf("inspection inferred a container execution platform: %s", result.Output)
			}
			if !test.environment && (output.NativePlatforms == nil || len(output.NativePlatforms) != 0 || strings.Contains(string(result.Output), `"dependencies":`)) {
				t.Fatal("Docker-only inspection did not preserve the empty platform list and absent dependencies")
			}
			wantRequests := []fakes.InspectionRequest{
				{URL: "https://api.github.com/repos/other/worker-agent/releases/tags/v1.2.3", Accept: "application/vnd.github+json", HasAuthorization: true, Authenticated: true},
				{URL: "https://api.github.com/repos/other/worker-agent/releases/assets/77", Accept: "application/octet-stream", HasAuthorization: true, Authenticated: true},
			}
			if !reflect.DeepEqual(source.Requests(), wantRequests) {
				t.Fatalf("private inspection did not use the fixed metadata/manifest endpoints: %+v", source.Requests())
			}
			wantSecretRequests := 1
			if test.environment {
				wantSecretRequests = 0
			}
			if secretRequests != wantSecretRequests || !source.CredentialsCleared() || strings.Contains(string(result.Output), token) {
				t.Fatal("inspection ignored credential precedence, retained temporary credentials or exposed them in metadata")
			}
		})
	}
}

func TestInspectAgentRejectsInvalidManifestWithoutExposingItsContents(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	source := fakes.NewInspectionSource("private-token-sentinel: malformed\n", "")
	resolver := fixtures.InspectionResolver(t, source)
	executor := fixtures.InspectAction(t, resolver, runtimes.RuntimeDocker)

	result, err := executor.Run(t.Context(), json.RawMessage(`{"package":"github.com/other/worker-agent@v1.2.3"}`))

	if err == nil || !strings.Contains(err.Error(), "invalid package manifest") || strings.Contains(err.Error(), "private-token-sentinel") {
		t.Fatalf("inspection did not safely reject invalid metadata: %v", err)
	}
	if len(result.Output) != 0 || len(result.Artifacts) != 0 || len(result.NextActions) != 0 || len(source.Requests()) != 1 {
		t.Fatal("failed inspection produced data or continued beyond its manifest read")
	}
}

func TestInspectAgentPreservesCancellationDuringMetadataDownload(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	requests := 0
	resolver := packages.NewResolver(fakes.Transport(func(request *http.Request) (*http.Response, error) {
		requests++
		cancel()
		return nil, request.Context().Err()
	}))
	t.Cleanup(resolver.CloseIdleConnections)
	executor := fixtures.InspectAction(t, resolver, runtimes.RuntimeDocker)

	result, err := executor.Run(ctx, json.RawMessage(`{"package":"github.com/other/worker-agent@v1.2.3"}`))

	if !errors.Is(err, context.Canceled) || requests != 1 || len(result.Output) != 0 {
		t.Fatalf("metadata cancellation was lost or allowed inspection to continue: %v", err)
	}
}
