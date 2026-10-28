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
	"github.com/defos-ai/commander/apps/agents/builder/tests/fakes"
	"github.com/defos-ai/commander/apps/agents/builder/tests/fixtures"
	"github.com/defos-ai/commander/framework/agent/action"
	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestBuildAgentValidatesAndExportsSource(t *testing.T) {
	source, _ := fixtures.Template(t)
	root := t.TempDir()
	checkout := runCheckout(t, fixtures.LocalCheckout(t, root, source))
	workspace := filepath.Join(root, checkout.WorkspaceID, "source")
	fixtures.WriteBuildFile(t, workspace, "go.mod", "module example.invalid/fixture\n\ngo 1.26.7\n")
	for _, name := range []string{".env", ".env.production", "state/runtime.json", ".cache/build.bin"} {
		fixtures.WriteBuildFile(t, workspace, name, "excluded deployment state")
	}
	for _, name := range []string{"GITHUB_TOKEN", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "COMMANDER_MCP_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY"} {
		t.Setenv(name, "ambient-secret-must-not-reach-child")
	}
	executor, config, _ := fixtures.BuildExecutor(t, root, checkout.WorkspaceID, "")
	input := fixtures.BuildInput(t, checkout.WorkspaceID)
	assertBuildInputSchema(t, executor.Action().InputSchema, input)
	ctx := fixtures.BuildContext(t)

	result, err := executor.Run(ctx, input)

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
	if output.AgentType != fixtures.BuildAgentType || output.ModulePath != "example.invalid/fixture" || output.Binary != fixtures.BuildBinary {
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
		if strings.HasPrefix(name, ".git/") || strings.HasPrefix(name, ".env") || strings.HasPrefix(name, "state/") || strings.HasPrefix(name, ".cache/") || strings.HasPrefix(name, ".build-") || name == "agent.yaml" || strings.HasPrefix(name, fixtures.BuildBinary+"_") || strings.Contains(contents, fixtures.BuildSecret) {
			t.Fatalf("archive retained excluded data: %s", name)
		}
	}
	for _, name := range []string{"agent.go", "LICENSE", "AGENTS.md", "docker/Dockerfile", ".github/scripts/release.go", ".github/workflows/make-release.yml", ".github/workflows/test.yml", ".github/workflows/build-binaries.yml", ".github/workflows/push.yml", ".github/workflows/push-stable.yml"} {
		if files[name] == "" {
			t.Errorf("verified release source missing from artifact: %s", name)
		}
	}
	calls, err := os.ReadFile(config.Calls)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 7 || !strings.HasPrefix(lines[0], "codex exec ") || lines[1] != "go test -count=1 -timeout=2m ./..." || !strings.HasPrefix(lines[2], "go build -o ") || lines[3] != "go mod edit -json" || !strings.HasPrefix(lines[4], "go run ./.github/scripts/release.go metadata ") || !strings.HasPrefix(lines[5], "go run ./.github/scripts/release.go manifest ") || !strings.HasPrefix(filepath.Base(strings.TrimPrefix(lines[6], "go run ")), "action-contracts-") || !strings.HasSuffix(lines[6], ".go") {
		t.Fatalf("build did not verify before exporting: %q", calls)
	}
	assertBuildScratchRemoved(t, config)
}

func TestBuildAgentVerifiesRegisteredActionContracts(t *testing.T) {
	for _, mode := range []string{
		"contracts-equivalent", "contracts-unexpected", "contracts-duplicate",
		"contracts-description", "contracts-input-schema", "contracts-output-schema", "contracts-number-precision",
		"contracts-secret", "contracts-duplicate-secret", "contracts-wrong-agent", "contracts-malformed",
		"contracts-trailing", "contracts-unknown-field", "contracts-oversize", "contracts-fail",
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, workspaceID := fixtures.BuildWorkspace(t)
			executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, mode)

			result, err := executor.Run(fixtures.BuildContext(t), fixtures.BuildInput(t, workspaceID))

			if mode == "contracts-equivalent" {
				if err != nil || len(result.Artifacts) != 1 {
					t.Fatalf("equivalent registered contracts did not produce source: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("build accepted invalid or mismatched registered action contracts")
				}
				if len(result.Output) != 0 || len(result.Artifacts) != 0 || len(result.NextActions) != 0 || strings.Contains(err.Error(), fixtures.BuildSecret) || strings.Contains(err.Error(), "untrusted-sensitive-build-diagnostic") || strings.Contains(err.Error(), root) {
					t.Fatalf("rejected contracts exposed output or raw diagnostics: %v", err)
				}
			}
			calls, err := os.ReadFile(config.Calls)
			if err != nil || strings.Count(string(calls), "\n") != 7 {
				t.Fatalf("contracts were not checked after ordinary build and release validation: %s (%v)", calls, err)
			}
			if _, err := os.Stat(filepath.Join(config.Source, "README.md")); err != nil {
				t.Fatal("contract verification removed the source needed for correction")
			}
			assertBuildScratchRemoved(t, config)
		})
	}
}

func TestBuildAgentRejectsInvalidActionContractsBeforeExecution(t *testing.T) {
	root, workspaceID := fixtures.BuildWorkspace(t)
	executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, "")
	for _, scenario := range []struct {
		name   string
		change func(map[string]any, map[string]any)
	}{
		{"missing actions", func(input, _ map[string]any) { delete(input, "actions") }},
		{"empty actions", func(input, _ map[string]any) { input["actions"] = []any{} }},
		{"null actions", func(input, _ map[string]any) { input["actions"] = nil }},
		{"duplicate names", func(input, definition map[string]any) { input["actions"] = []any{definition, definition} }},
		{"unsafe name", func(_, definition map[string]any) { definition["name"] = "../count-words" }},
		{"blank description", func(_, definition map[string]any) { definition["description"] = " \t " }},
		{"invalid secret name", func(_, definition map[string]any) { definition["required_secrets"] = []string{"service/token"} }},
		{"missing object type", func(_, definition map[string]any) { definition["input_schema"] = map[string]any{} }},
		{"invalid schema type", func(_, definition map[string]any) {
			definition["output_schema"] = map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "not-a-json-type"}}}
		}},
		{"unresolved schema reference", func(_, definition map[string]any) {
			definition["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"$ref": "#/$defs/missing"}}}
		}},
		{"remote schema reference", func(_, definition map[string]any) {
			definition["input_schema"] = map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"$ref": "https://schema.invalid/text"}}}
		}},
		{"runtime readiness field", func(_, definition map[string]any) { definition["status"] = "ready" }},
		{"unknown contract field", func(_, definition map[string]any) { definition["instruction"] = "implement" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var input map[string]any
			if err := json.Unmarshal(fixtures.BuildInput(t, workspaceID), &input); err != nil {
				t.Fatal(err)
			}
			definition := input["actions"].([]any)[0].(map[string]any)
			scenario.change(input, definition)
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}

			result, err := executor.Run(t.Context(), raw)

			if err == nil || errors.Is(err, action.ErrSecretUnavailable) {
				t.Fatalf("action contract was not rejected before managed-secret access: %v", err)
			}
			if len(result.Output) != 0 || len(result.Artifacts) != 0 || len(result.NextActions) != 0 {
				t.Fatal("invalid contract produced source or output")
			}
			if _, err := os.Stat(config.Calls); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid contract started a subprocess")
			}
			entries, err := os.ReadDir(filepath.Dir(config.Source))
			if err != nil || len(entries) != 1 || entries[0].Name() != "source" {
				t.Fatal("invalid contract created build scratch files")
			}
			contents, err := os.ReadFile(filepath.Join(config.Source, "README.md"))
			if err != nil || string(contents) != "Local source fixture\n" {
				t.Fatal("invalid contract changed the source checkout")
			}
		})
	}
}

func assertBuildInputSchema(t *testing.T, rawSchema, input json.RawMessage) {
	t.Helper()
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(input, &value); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(value); err != nil {
		t.Fatal("advertised schema rejected structured contracts with local references and extensions:", err)
	}
	definition := value["actions"].([]any)[0].(map[string]any)
	definition["status"] = "ready"
	if err := resolved.Validate(value); err == nil {
		t.Fatal("advertised schema accepts runtime readiness in an action contract")
	}
	delete(value, "actions")
	if err := resolved.Validate(value); err == nil {
		t.Fatal("advertised schema does not require action contracts")
	}
}

func TestBuildAgentProtectsManagedSecretsAndFailedOutput(t *testing.T) {
	for _, scenario := range []string{"missing secret", "empty secret", "codex failure", "Go tests fail", "Go build fails", "secret in source", "secret in filename", "symlink in source"} {
		t.Run(scenario, func(t *testing.T) {
			root, workspaceID := fixtures.BuildWorkspace(t)
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
			executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, mode)
			t.Setenv("CODEX_API_KEY", "ambient-value-is-not-a-managed-secret")
			t.Setenv("OPENAI_API_KEY", "ambient-value-is-not-a-managed-secret")
			ctx := t.Context()
			if scenario == "empty secret" {
				ctx = action.WithSecrets(ctx, fakes.BuildSecrets(""))
			}
			if mode != "" {
				ctx = fixtures.BuildContext(t)
			}

			result, err := executor.Run(ctx, fixtures.BuildInput(t, workspaceID))

			if err == nil {
				t.Fatal("unsafe or unsuccessful build returned success")
			}
			if len(result.Output) != 0 || len(result.Artifacts) != 0 || strings.Contains(err.Error(), fixtures.BuildSecret) || strings.Contains(err.Error(), "untrusted-sensitive-build-diagnostic") {
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
				wantCalls := 7
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
		{"unguarded-release", "initial commit guard"},
		{"missing-package-descriptions", "responsibilities and actions"},
		{"stale-manifest-actions", "action"},
	} {
		t.Run(scenario.mode, func(t *testing.T) {
			root, workspaceID := fixtures.BuildWorkspace(t)
			executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, scenario.mode)

			result, err := executor.Run(fixtures.BuildContext(t), fixtures.BuildInput(t, workspaceID))

			if err == nil {
				t.Fatal("build accepted source that cannot produce a compatible release")
			}
			if !strings.Contains(err.Error(), scenario.reason) {
				t.Fatalf("release validation did not identify the correction needed: %v", err)
			}
			if len(result.Output) != 0 || len(result.Artifacts) != 0 || len(result.NextActions) != 0 || strings.Contains(err.Error(), fixtures.BuildSecret) || strings.Contains(err.Error(), "untrusted-sensitive-build-diagnostic") || strings.Contains(err.Error(), root) {
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
	root, workspaceID := fixtures.BuildWorkspace(t)
	executor, config, scope := fixtures.BuildExecutor(t, root, workspaceID, "")
	for _, value := range []string{"../checkout-123", "/tmp/checkout-123", "checkout-abc", "checkout-999999999"} {
		if _, err := executor.Run(fixtures.BuildContext(t), fixtures.BuildInput(t, value)); err == nil {
			t.Fatalf("accepted invalid workspace %q", value)
		}
	}
	for _, name := range []string{"root", "source"} {
		t.Run("symlink "+name, func(t *testing.T) {
			linkedRoot, linkedID := fixtures.BuildWorkspace(t)
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
			if _, err := unsafe.Run(fixtures.BuildContext(t), fixtures.BuildInput(t, linkedID)); err == nil {
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
	if _, err := executor.Run(fixtures.BuildContext(t), fixtures.BuildInput(t, workspaceID)); err == nil {
		t.Fatal("rediscovery retained stale executables")
	}
	if _, err := os.Stat(config.Calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected build executed a child")
	}
}

func TestBuildAgentCancellationPreservesCheckout(t *testing.T) {
	for _, stage := range []struct {
		mode  string
		calls int
	}{{"wait", 1}, {"contracts-wait", 7}} {
		t.Run(stage.mode, func(t *testing.T) {
			testBuildAgentCancellation(t, stage.mode, stage.calls)
		})
	}
}

func testBuildAgentCancellation(t *testing.T, mode string, expectedCalls int) {
	t.Helper()
	root, workspaceID := fixtures.BuildWorkspace(t)
	executor, config, _ := fixtures.BuildExecutor(t, root, workspaceID, mode)
	ctx, cancel := context.WithCancel(fixtures.BuildContext(t))
	defer cancel()
	type outcome struct {
		result action.Result
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := executor.Run(ctx, fixtures.BuildInput(t, workspaceID))
		finished <- outcome{result, err}
	}()
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
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(config.Started); err == nil {
			break
		}
		select {
		case result := <-finished:
			stopped = true
			t.Fatalf("build stopped before cancellation: %v", result.err)
		case <-deadline.C:
			t.Fatal("build stage fixture did not start")
		case <-tick.C:
		}
	}
	cancel()
	select {
	case result := <-finished:
		stopped = true
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("build lost cancellation: %v", result.err)
		}
		if len(result.result.Artifacts) != 0 || len(result.result.Output) != 0 || len(result.result.NextActions) != 0 {
			t.Fatal("cancelled build returned source or output")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("build child did not stop")
	}
	calls, err := os.ReadFile(config.Calls)
	if err != nil || strings.Count(string(calls), "\n") != expectedCalls {
		t.Fatal("cancelled build ran later verification stages")
	}
	if _, err := os.Stat(filepath.Join(config.Source, "README.md")); err != nil {
		t.Fatal("cancellation removed the source checkout")
	}
	assertBuildScratchRemoved(t, config)
}

func assertBuildScratchRemoved(t *testing.T, config fakes.BuildConfig) {
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
