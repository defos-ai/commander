package builder_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/defos-ai/commander/framework/agent/environment"
	"github.com/defos-ai/commander/framework/agent/manifest"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

const buildTestSecret = "managed-codex-token-not-for-artifacts"
const buildTestInstructions = "Create an agent that counts words. $(never-run-this)\nPreserve \"quoted\" instructions."
const buildTestAgentType = "word-count-agent"
const buildTestBinary = "commander-word-count-agent"

func buildRequestedActions() []actionmodel.Definition {
	return []actionmodel.Definition{{
		Name: "count-words", Description: "Return the word count of supplied text",
		RequiredSecrets: []string{"SERVICE_TOKEN"},
		InputSchema:     json.RawMessage(`{"type":"object","$defs":{"text":{"type":"string","minLength":1}},"properties":{"text":{"$ref":"#/$defs/text"},"options":{"type":"object","properties":{"language":{"type":"string","default":"en"}},"additionalProperties":false}},"required":["text"],"additionalProperties":false,"x-contract":{"sample":9007199254740993,"type":"business-label"}}`),
		OutputSchema:    json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","minimum":0}},"required":["count"],"additionalProperties":false}`),
	}}
}

type buildFixtureConfig struct {
	Source, Mode, Started, Calls, Home string
}

func buildFixtureScope(t *testing.T, config buildFixtureConfig) *environment.DiscoveryScope {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"codex", "go"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path := filepath.Join(bin, name)
		if runtime.GOOS == "windows" {
			if err := copyGitFixture(self, path); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Link(self, path); err != nil {
			if err := copyGitFixture(self, path); err != nil {
				t.Fatal(err)
			}
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "build-fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return &environment.DiscoveryScope{Paths: []string{bin}}
}

func runBuildFixture(tool string) int {
	if err := executeBuildFixture(tool); err != nil {
		fmt.Fprintln(os.Stderr, "Build fixture:", err)
		return 7
	}
	return 0
}

func executeBuildFixture(tool string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(self), "build-fixture.json"))
	if err != nil {
		return err
	}
	var config buildFixtureConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	actual, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	expected, err := filepath.EvalSymlinks(config.Source)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("child ran outside the selected source directory")
	}
	for _, key := range []string{"GITHUB_TOKEN", "SECRETS_ENCRYPTION_KEY", "API_PASSWORD", "COMMANDER_MCP_TOKEN", "OPENAI_API_KEY"} {
		if _, exists := os.LookupEnv(key); exists {
			return fmt.Errorf("child inherited %s", key)
		}
	}
	if os.Getenv("HOME") == "" {
		return errors.New("missing private child home")
	}
	if tool == "codex" {
		if os.Getenv("CODEX_HOME") == "" {
			return errors.New("Codex did not receive isolated configuration storage")
		}
		if os.Getenv("CODEX_API_KEY") != buildTestSecret {
			return errors.New("Codex did not receive its managed key")
		}
		args := os.Args[1:]
		if len(args) == 0 || args[0] != "exec" || args[len(args)-1] != "-" {
			return errors.New("Codex was not given a stdin prompt")
		}
		if !slices.Contains(args, "--ephemeral") {
			return errors.New("Codex session must be ephemeral")
		}
		if index := slices.Index(args, "--sandbox"); index < 0 || index+1 >= len(args) || args[index+1] != "workspace-write" {
			return errors.New("Codex sandbox was not scoped to the workspace")
		}
		prompt, err := io.ReadAll(io.LimitReader(os.Stdin, 2<<20))
		if err != nil {
			return err
		}
		if err := validateBuildPrompt(prompt); err != nil {
			return err
		}
		if config.Home != "" {
			if err := os.WriteFile(config.Home, []byte(os.Getenv("HOME")), 0600); err != nil {
				return err
			}
		}
	} else {
		if _, exists := os.LookupEnv("CODEX_API_KEY"); exists {
			return errors.New("Go inherited the model key")
		}
		if os.Getenv("GOWORK") != "off" {
			return errors.New("Go inherited workspace module wiring")
		}
	}
	if err := appendBuildCall(config.Calls, tool+" "+strings.Join(os.Args[1:], " ")); err != nil {
		return err
	}
	if tool == "codex" {
		switch config.Mode {
		case "wait":
			if err := os.WriteFile(config.Started, []byte("started"), 0600); err != nil {
				return err
			}
			time.Sleep(30 * time.Second)
			return errors.New("cancelled child kept running")
		case "fail":
			fmt.Fprintln(os.Stdout, buildTestSecret)
			return errors.New("untrusted-sensitive-build-diagnostic: " + buildTestSecret)
		}
		if err := os.MkdirAll(filepath.Join(cwd, "cmd"), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cwd, "cmd", "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
			return err
		}
		if err := writeReleaseFixture(cwd, config.Mode); err != nil {
			return err
		}
		if config.Mode == "leak" {
			return os.WriteFile(filepath.Join(cwd, "leaked.txt"), []byte(buildTestSecret), 0600)
		}
		if config.Mode == "filename-leak" {
			return os.WriteFile(filepath.Join(cwd, buildTestSecret+".txt"), []byte("clean contents"), 0600)
		}
		return nil
	}
	args := os.Args[1:]
	if slices.Equal(args, []string{"test", "-count=1", "-timeout=2m", "./..."}) {
		if config.Mode == "test-fail" {
			return errors.New("untrusted-sensitive-build-diagnostic: " + buildTestSecret)
		}
		return nil
	}
	if len(args) == 4 && args[0] == "build" && args[1] == "-o" && args[3] == "./cmd" {
		if config.Mode == "build-fail" {
			return errors.New("untrusted-sensitive-build-diagnostic: " + buildTestSecret)
		}
		if relative, err := filepath.Rel(config.Source, args[2]); err != nil || filepath.IsLocal(relative) {
			return errors.New("validation binary must stay outside source")
		}
		return os.WriteFile(args[2], []byte("fixture executable"), 0700)
	}
	if slices.Equal(args, []string{"mod", "edit", "-json"}) {
		module := map[string]any{"Module": map[string]string{"Path": "example.invalid/fixture"}}
		if config.Mode == "local-replace" {
			module["Replace"] = []any{map[string]any{"Old": map[string]string{"Path": "example.invalid/dependency"}, "New": map[string]string{"Path": "../dependency"}}}
		}
		return json.NewEncoder(os.Stdout).Encode(module)
	}
	if len(args) >= 3 && args[0] == "run" && args[1] == "./.github/scripts/release.go" {
		return runReleaseFixture(args[2:], config)
	}
	return errors.New("unexpected Go verification command")
}

func validateBuildPrompt(prompt []byte) error {
	text := string(prompt)
	if !strings.Contains(text, "Requested agent:\n"+buildTestInstructions+"\n\nRequested action contracts (JSON):\n") || strings.Contains(text, buildTestSecret) {
		return errors.New("stdin lost instructions or included credentials")
	}
	_, contracts, ok := strings.Cut(text, "Requested action contracts (JSON):\n")
	if !ok {
		return errors.New("stdin did not separate requested action contracts from instructions")
	}
	var actual []actionmodel.Definition
	decoder := json.NewDecoder(strings.NewReader(contracts))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&actual); err != nil {
		return fmt.Errorf("decode requested action contracts: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("stdin has trailing content after action contracts")
	}
	expected := buildRequestedActions()
	if len(actual) != len(expected) {
		return errors.New("stdin lost requested action contracts")
	}
	for i, want := range expected {
		got := actual[i]
		if got.Name != want.Name || got.Description != want.Description || !slices.Equal(got.RequiredSecrets, want.RequiredSecrets) {
			return errors.New("stdin changed requested action metadata")
		}
		for _, pair := range [][2]json.RawMessage{{got.InputSchema, want.InputSchema}, {got.OutputSchema, want.OutputSchema}} {
			var gotCompact, wantCompact bytes.Buffer
			// RawMessage keeps extension keywords and large JSON integers intact.
			if json.Compact(&gotCompact, pair[0]) != nil || json.Compact(&wantCompact, pair[1]) != nil || !bytes.Equal(gotCompact.Bytes(), wantCompact.Bytes()) {
				return errors.New("stdin changed requested JSON schemas")
			}
		}
	}
	return nil
}

func writeReleaseFixture(root, mode string) error {
	agentType := buildTestAgentType
	if mode == "template-identity" {
		agentType = "example-agent"
	}
	files := map[string]string{
		"agent.go":                   "package generated\nconst AgentType = " + fmt.Sprintf("%q", agentType) + "\n",
		"LICENSE":                    "MIT License\n",
		"AGENTS.md":                  "Keep action implementations in actions and tests in tests.\n",
		"agent.yaml.tmpl":            "schema_version: 1\ntype: {{.Type}}\nversion: {{.Version}}\n",
		"docker/Dockerfile":          "FROM scratch\n",
		".github/scripts/release.go": "package main\nfunc main() {}\n",
	}
	for _, name := range []string{"make-release", "test", "build-binaries", "push", "push-stable"} {
		files[".github/workflows/"+name+".yml"] = "name: release fixture\non: workflow_dispatch\njobs: {}\n"
	}
	files[".github/workflows/make-release.yml"] = buildReleaseWorkflowFixture
	if mode == "unguarded-release" {
		files[".github/workflows/make-release.yml"] = strings.Replace(buildReleaseWorkflowFixture, `test "$GITHUB_SHA" = "$EXPECTED_COMMIT"`, "true", 1)
	}
	if mode == "local-replace" {
		files["go.mod"] = "module example.invalid/fixture\n\ngo 1.26.7\n\nreplace example.invalid/dependency => ../dependency\n"
	}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			return err
		}
	}
	return nil
}

const buildReleaseWorkflowFixture = `name: Make release
run-name: Release agent ${{ inputs.version }} ${{ inputs.request_id }}
on:
  workflow_dispatch:
    inputs:
      version: {type: string, required: true}
      expected_commit: {type: string, required: false}
      request_id: {type: string, required: false}
jobs:
  prepare:
    runs-on: ubuntu-latest
    steps:
      - name: Verify requested commit
        env:
          EXPECTED_COMMIT: ${{ inputs.expected_commit }}
        run: |
          if [ -n "$EXPECTED_COMMIT" ]; then
            test "$GITHUB_SHA" = "$EXPECTED_COMMIT"
          fi
      - uses: actions/checkout@v7
        with:
          ref: ${{ github.sha }}
  tests:
    needs: prepare
    uses: ./.github/workflows/test.yml
  binaries:
    needs: [prepare, tests]
    uses: ./.github/workflows/build-binaries.yml
  image:
    needs: [tests, binaries]
    uses: ./.github/workflows/push.yml
  publish:
    needs: [prepare, binaries, image]
    runs-on: ubuntu-latest
    steps:
      - run: echo publish fixture
  stable:
    needs: [prepare, image, publish]
    uses: ./.github/workflows/push-stable.yml
`

func runReleaseFixture(args []string, config buildFixtureConfig) error {
	if len(args) < 5 || args[1] != "--version" || args[2] != "v0.1.0" || args[3] != "--repository" || args[4] != "release-check/generated-agent" {
		return errors.New("release helper was not checked with the reserved dry-run identity")
	}
	const image = "ghcr.io/release-check/" + buildTestBinary
	if args[0] == "metadata" && len(args) == 5 {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"version": "v0.1.0", "type": buildTestAgentType, "binary": buildTestBinary, "image": image})
	}
	if args[0] != "manifest" || len(args) != 9 || args[5] != "--digest" || args[6] != "sha256:"+strings.Repeat("0", 64) || args[7] != "--assets" {
		return errors.New("unexpected manifest verification command")
	}
	assets := args[8]
	if relative, err := filepath.Rel(config.Source, assets); err != nil || filepath.IsLocal(relative) || !filepath.IsAbs(assets) {
		return errors.New("dry-run release assets must stay outside source")
	}
	value := manifest.Manifest{SchemaVersion: 1, Type: buildTestAgentType, Version: "v0.1.0", Native: map[string]manifest.NativeAsset{}, Docker: &manifest.Docker{Image: image + "@" + args[6]}}
	if config.Mode != "missing-package-descriptions" {
		value.Responsibilities = []string{"Count words in supplied text"}
		value.Actions = []manifest.Action{{Name: "count-words", Description: "Return the word count of supplied text"}}
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			asset := buildTestBinary + "_" + goos + "_" + arch
			if goos == "windows" {
				asset += ".exe"
			}
			data, err := os.ReadFile(filepath.Join(assets, asset))
			if err != nil {
				return err
			}
			if len(data) == 0 {
				return errors.New("release probe asset must be nonempty")
			}
			value.Native[goos+"/"+arch] = manifest.NativeAsset{Asset: asset, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
		}
	}
	data, err := manifest.Render(value)
	if err != nil {
		return err
	}
	if config.Mode == "malformed-manifest" {
		data = []byte("schema_version: 1\ntype: example-agent\nunknown: untrusted-sensitive-build-diagnostic\n")
	}
	return os.WriteFile(filepath.Join(assets, "agent.yaml"), data, 0600)
}

func appendBuildCall(path, value string) (err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	_, err = fmt.Fprintln(file, value)
	return err
}
