package builder_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	builderactions "github.com/defos-ai/commander/apps/agents/builder/actions"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
)

func TestBuildAgentValidatesAndExportsSource(t *testing.T) {
	source, _ := templateFixture(t)
	root := t.TempDir()
	checkout := runCheckout(t, localCheckout(t, root, source))
	workspace := filepath.Join(root, checkout.WorkspaceID, "source")
	writeBuildFile(t, workspace, "go.mod", "module example.invalid/fixture\n\ngo 1.26.7\n")
	for _, name := range []string{".env", ".env.production", "state/runtime.json", ".cache/build.bin"} {
		writeBuildFile(t, workspace, name, "excluded deployment state")
	}
	for _, name := range []string{"GITHUB_TOKEN", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "COMMANDER_MCP_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY"} {
		t.Setenv(name, "ambient-secret-must-not-reach-child")
	}
	executor, config, _ := buildExecutor(t, root, checkout.WorkspaceID, "")
	result, err := executor.Run(buildSecretContext(t), buildInput(t, checkout.WorkspaceID))
	if err != nil {
		t.Fatal(err)
	}
	var output builderactions.BuildAgentOutput
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	if output.WorkspaceID != checkout.WorkspaceID || bytes.Contains(result.Output, []byte(root)) || len(result.NextActions) != 0 {
		t.Fatalf("unexpected build result: %s", result.Output)
	}
	if output.AgentType != buildTestAgentType || output.ModulePath != "example.invalid/fixture" || output.Binary != buildTestBinary {
		t.Fatalf("build did not return verified release identity: %+v", output)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].Name != "Agent source" || len(result.Artifacts[0].Files) != 1 {
		t.Fatal("build must return one source artifact")
	}
	file := result.Artifacts[0].Files[0]
	if file.Name != "source.tar.gz" || file.Data == nil || file.Path != "" {
		t.Fatal("source archive must be returned as artifact bytes")
	}
	files := readBuildArchive(t, file.Data)
	if output.SourceFiles != len(files) || files["cmd/main.go"] == "" || files["go.mod"] == "" || files["README.md"] == "" {
		t.Fatalf("archive lost verified source: files=%v output=%+v", files, output)
	}
	for name, contents := range files {
		if strings.HasPrefix(name, ".git/") || strings.HasPrefix(name, ".env") || strings.HasPrefix(name, "state/") || strings.HasPrefix(name, ".cache/") || strings.HasPrefix(name, ".build-") || name == "agent.yaml" || strings.HasPrefix(name, buildTestBinary+"_") || strings.Contains(contents, buildTestSecret) {
			t.Fatalf("archive retained excluded data: %s", name)
		}
	}
	for _, name := range []string{"agent.go", "LICENSE", "AGENTS.md", "agent.yaml.tmpl", "docker/Dockerfile", ".github/scripts/release.go", ".github/workflows/make-release.yml", ".github/workflows/test.yml", ".github/workflows/build-binaries.yml", ".github/workflows/push.yml", ".github/workflows/push-stable.yml"} {
		if files[name] == "" {
			t.Errorf("verified release source missing from artifact: %s", name)
		}
	}
	calls, err := os.ReadFile(config.Calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 6 || !strings.HasPrefix(lines[0], "codex exec ") || lines[1] != "go test -count=1 -timeout=2m ./..." || !strings.HasPrefix(lines[2], "go build -o ") || lines[3] != "go mod edit -json" || !strings.HasPrefix(lines[4], "go run ./.github/scripts/release.go metadata ") || !strings.HasPrefix(lines[5], "go run ./.github/scripts/release.go manifest ") {
		t.Fatalf("build did not verify before exporting: %q", calls)
	}
	assertBuildScratchRemoved(t, config)
}

func TestBuildAgentProtectsManagedSecretsAndFailedOutput(t *testing.T) {
	for _, scenario := range []string{"missing secret", "empty secret", "codex failure", "Go tests fail", "Go build fails", "secret in source", "secret in filename", "symlink in source"} {
		t.Run(scenario, func(t *testing.T) {
			root, workspaceID := buildWorkspace(t)
			mode := ""
			if scenario == "codex failure" {
				mode = "fail"
			}
			if scenario == "secret in source" {
				mode = "leak"
			}
			if scenario == "secret in filename" {
				mode = "filename-leak"
			}
			if scenario == "Go tests fail" {
				mode = "test-fail"
			}
			if scenario == "Go build fails" {
				mode = "build-fail"
			}
			if scenario == "symlink in source" {
				mode = "symlink"
				if err := os.Symlink("README.md", filepath.Join(root, workspaceID, "source", "linked-readme")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			executor, config, _ := buildExecutor(t, root, workspaceID, mode)
			t.Setenv("CODEX_API_KEY", "ambient-value-is-not-a-managed-secret")
			ctx := t.Context()
			if scenario == "empty secret" {
				ctx = action.WithSecrets(ctx, buildSecrets(""))
			}
			if mode != "" {
				ctx = buildSecretContext(t)
			}
			result, err := executor.Run(ctx, buildInput(t, workspaceID))
			if err == nil {
				t.Fatal("unsafe or unsuccessful build returned success")
			}
			if len(result.Output) != 0 || len(result.Artifacts) != 0 || strings.Contains(err.Error(), buildTestSecret) || strings.Contains(err.Error(), "untrusted-sensitive-build-diagnostic") {
				t.Fatal("failed build exposed raw diagnostics, source or credentials")
			}
			if mode == "" {
				if _, err := os.Stat(config.Calls); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("build started a process without its managed secret")
				}
			} else {
				assertBuildScratchRemoved(t, config)
				calls, err := os.ReadFile(config.Calls)
				if err != nil {
					t.Fatal(err)
				}
				wantCalls := 6
				if mode == "fail" {
					wantCalls = 1
				} else if mode == "test-fail" {
					wantCalls = 2
				} else if mode == "build-fail" {
					wantCalls = 3
				}
				if strings.Count(string(calls), "\n") != wantCalls {
					t.Fatalf("failed build ran the wrong verification stages: %s", calls)
				}
			}
			if _, err := os.Stat(filepath.Join(config.Source, "README.md")); err != nil {
				t.Fatal("failed build removed the existing source checkout")
			}
		})
	}
}

func TestBuildAgentRejectsSourceThatCannotRelease(t *testing.T) {
	for _, scenario := range []struct{ mode, reason string }{
		{"template-identity", "replace example-agent"},
		{"malformed-manifest", "invalid agent.yaml"},
		{"local-replace", "local replace directive"},
	} {
		t.Run(scenario.mode, func(t *testing.T) {
			root, workspaceID := buildWorkspace(t)
			executor, config, _ := buildExecutor(t, root, workspaceID, scenario.mode)
			result, err := executor.Run(buildSecretContext(t), buildInput(t, workspaceID))
			if err == nil {
				t.Fatal("build accepted source that cannot produce a compatible release")
			}
			if !strings.Contains(err.Error(), scenario.reason) {
				t.Fatalf("release validation did not identify the correction needed: %v", err)
			}
			if len(result.Output) != 0 || len(result.Artifacts) != 0 || len(result.NextActions) != 0 || strings.Contains(err.Error(), buildTestSecret) || strings.Contains(err.Error(), "untrusted-sensitive-build-diagnostic") || strings.Contains(err.Error(), root) {
				t.Fatalf("rejected release exposed output or raw diagnostics: %v", err)
			}
			if _, err := os.Stat(filepath.Join(config.Source, "README.md")); err != nil {
				t.Fatal("release validation removed the source needed for correction")
			}
			assertBuildScratchRemoved(t, config)
		})
	}
}

func TestBuildAgentRejectsUnsafeWorkspacesAndMissingTools(t *testing.T) {
	root, workspaceID := buildWorkspace(t)
	executor, config, scope := buildExecutor(t, root, workspaceID, "")
	for _, value := range []string{"../checkout-123", "/tmp/checkout-123", "checkout-abc", "checkout-999999999"} {
		if _, err := executor.Run(buildSecretContext(t), buildInput(t, value)); err == nil {
			t.Fatalf("accepted invalid workspace %q", value)
		}
	}
	for _, name := range []string{"root", "source"} {
		t.Run("symlink "+name, func(t *testing.T) {
			linkedRoot, linkedID := buildWorkspace(t)
			link := filepath.Join(linkedRoot, linkedID, "source")
			target := t.TempDir()
			if name == "root" {
				link = filepath.Join(t.TempDir(), "linked-workspaces")
				target, linkedRoot = linkedRoot, link
			} else if err := os.RemoveAll(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			unsafe, err := builderactions.NewBuildAgent(linkedRoot)
			if err != nil {
				return
			}
			if _, err := unsafe.Discover(t.Context(), &environment.Local{}, scope); err != nil {
				t.Fatal(err)
			}
			if _, err := unsafe.Run(buildSecretContext(t), buildInput(t, linkedID)); err == nil {
				t.Fatal("build followed a workspace symlink")
			}
		})
	}
	status, err := executor.Discover(t.Context(), &environment.Local{}, &environment.DiscoveryScope{Paths: []string{}})
	if err != nil || status != action.Unavailable {
		t.Fatalf("missing tools status=%s err=%v", status, err)
	}
	missing := map[string]bool{}
	for _, requirement := range executor.Action().MissingRequirements {
		missing[requirement.Name] = true
	}
	if !missing["codex"] || !missing["go"] {
		t.Fatalf("missing tools not advertised: %+v", executor.Action().MissingRequirements)
	}
	if _, err := executor.Run(buildSecretContext(t), buildInput(t, workspaceID)); err == nil {
		t.Fatal("rediscovery retained stale executables")
	}
	if _, err := os.Stat(config.Calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected build executed a child")
	}
}

func TestBuildAgentCancellationPreservesCheckout(t *testing.T) {
	root, workspaceID := buildWorkspace(t)
	executor, config, _ := buildExecutor(t, root, workspaceID, "wait")
	ctx, cancel := context.WithCancel(buildSecretContext(t))
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := executor.Run(ctx, buildInput(t, workspaceID)); finished <- err }()
	stopped := false
	t.Cleanup(func() {
		cancel()
		if stopped {
			return
		}
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("cancelled build did not stop during cleanup")
		}
	})
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(config.Started); err == nil {
			break
		}
		select {
		case err := <-finished:
			stopped = true
			t.Fatalf("build stopped before cancellation: %v", err)
		case <-deadline.C:
			t.Fatal("Codex fixture did not start")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case err := <-finished:
		stopped = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("build lost cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Codex child did not stop")
	}
	calls, err := os.ReadFile(config.Calls)
	if err != nil || strings.Count(string(calls), "\n") != 1 {
		t.Fatal("cancelled build ran later verification stages")
	}
	if _, err := os.Stat(filepath.Join(config.Source, "README.md")); err != nil {
		t.Fatal("cancellation removed the source checkout")
	}
	assertBuildScratchRemoved(t, config)
}

func buildExecutor(t *testing.T, root, workspaceID, mode string) (*builderactions.BuildAgent, buildFixtureConfig, *environment.DiscoveryScope) {
	t.Helper()
	control := t.TempDir()
	config := buildFixtureConfig{Source: filepath.Join(root, workspaceID, "source"), Mode: mode,
		Started: filepath.Join(control, "started"), Calls: filepath.Join(control, "calls"), Home: filepath.Join(control, "home")}
	scope := buildFixtureScope(t, config)
	executor, err := builderactions.NewBuildAgent(root)
	if err != nil {
		t.Fatal(err)
	}
	status, err := executor.Discover(t.Context(), &environment.Local{}, scope)
	if err != nil || status != action.Ready {
		t.Fatalf("build discovery: status=%s err=%v", status, err)
	}
	if names := executor.Action().RequiredSecrets; len(names) != 1 || names[0] != "CODEX_API_KEY" {
		t.Fatalf("build secret declaration: %v", names)
	}
	return executor, config, scope
}

func buildWorkspace(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	const id = "checkout-123456"
	source := filepath.Join(root, id, "source")
	writeBuildFile(t, source, "README.md", "Local source fixture\n")
	writeBuildFile(t, source, "go.mod", "module example.invalid/fixture\n\ngo 1.26.7\n")
	return root, id
}

func writeBuildFile(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func buildInput(t *testing.T, workspaceID string) json.RawMessage {
	t.Helper()
	input, err := json.Marshal(builderactions.BuildAgentInput{WorkspaceID: workspaceID, Instructions: buildTestInstructions})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

type buildSecrets string

func (s buildSecrets) Secret(name string) ([]byte, error) {
	if name != "CODEX_API_KEY" {
		return nil, action.ErrSecretUnavailable
	}
	return []byte(s), nil
}

func buildSecretContext(t *testing.T) context.Context {
	t.Helper()
	return action.WithSecrets(t.Context(), buildSecrets(buildTestSecret))
}

func assertBuildScratchRemoved(t *testing.T, config buildFixtureConfig) {
	t.Helper()
	home, err := os.ReadFile(config.Home)
	if err != nil {
		t.Fatal("child did not record its isolated home:", err)
	}
	if _, err := os.Stat(string(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("build retained its temporary child home")
	}
}

func readBuildArchive(t *testing.T, data []byte) map[string]string {
	t.Helper()
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	files := map[string]string{}
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg || !filepath.IsLocal(filepath.FromSlash(header.Name)) {
			t.Fatalf("unsafe archive entry %q", header.Name)
		}
		contents, err := io.ReadAll(io.LimitReader(archive, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		files[header.Name] = string(contents)
	}
	return files
}
