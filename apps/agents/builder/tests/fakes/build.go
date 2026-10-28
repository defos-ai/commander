package fakes

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/defos-ai/commander/framework/agent/manifest"
	actionmodel "github.com/defos-ai/commander/framework/domain/actions"
)

type BuildProtocol struct {
	Secret, Instructions, AgentType, Binary, ReleaseWorkflow string
	Actions                                                  []actionmodel.Definition
}

type BuildConfig struct {
	Protocol                                              BuildProtocol
	Source, Mode, Started, Calls, Home                    string
	ProbeGo, ProbeWorkspace, ProbeModuleCache, ProbeCache string
}

func RunBuild(tool string) int {
	if err := executeBuild(tool); err != nil {
		fmt.Fprintln(os.Stderr, "Build fixture:", err)
		return 7
	}
	return 0
}

func executeBuild(tool string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(self), "build-fixture.json"))
	if err != nil {
		return err
	}
	var config BuildConfig
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
		if os.Getenv("CODEX_API_KEY") != config.Protocol.Secret {
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
		if err := validateBuildPrompt(prompt, config); err != nil {
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
			fmt.Fprintln(os.Stdout, config.Protocol.Secret)
			return errors.New("untrusted-sensitive-build-diagnostic: " + config.Protocol.Secret)
		}
		if err := os.MkdirAll(filepath.Join(cwd, "cmd"), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cwd, "cmd", "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
			return err
		}
		if err := writeRelease(cwd, config); err != nil {
			return err
		}
		if config.Mode == "leak" {
			return os.WriteFile(filepath.Join(cwd, "leaked.txt"), []byte(config.Protocol.Secret), 0600)
		}
		if config.Mode == "filename-leak" {
			return os.WriteFile(filepath.Join(cwd, config.Protocol.Secret+".txt"), []byte("clean contents"), 0600)
		}
		return nil
	}
	args := os.Args[1:]
	if slices.Equal(args, []string{"test", "-count=1", "-timeout=2m", "./..."}) {
		if config.Mode == "test-fail" {
			return errors.New("untrusted-sensitive-build-diagnostic: " + config.Protocol.Secret)
		}
		return nil
	}
	if len(args) == 4 && args[0] == "build" && args[1] == "-o" && args[3] == "./cmd" {
		if config.Mode == "build-fail" {
			return errors.New("untrusted-sensitive-build-diagnostic: " + config.Protocol.Secret)
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
	if len(args) == 2 && args[0] == "run" && strings.HasPrefix(filepath.Base(args[1]), "action-contracts-") && strings.HasSuffix(args[1], ".go") {
		return runBuildContract(args[1], config)
	}
	if len(args) >= 3 && args[0] == "run" && args[1] == "./.github/scripts/release.go" {
		return runRelease(args[2:], config)
	}
	return errors.New("unexpected Go verification command")
}

func runBuildContract(probe string, config BuildConfig) error {
	if relative, err := filepath.Rel(config.Source, probe); err != nil || filepath.IsLocal(relative) || !filepath.IsAbs(probe) {
		return errors.New("action contract probe must stay outside source")
	}
	if config.ProbeGo != "" {
		return runRealContractProbe([]string{"run", probe}, config)
	}
	switch config.Mode {
	case "contracts-wait":
		if err := os.WriteFile(config.Started, []byte("started"), 0600); err != nil {
			return err
		}
		time.Sleep(30 * time.Second)
		return errors.New("cancelled contract probe kept running")
	case "contracts-fail":
		fmt.Fprintln(os.Stdout, config.Protocol.Secret)
		return errors.New("untrusted-sensitive-build-diagnostic: " + config.Protocol.Secret)
	case "contracts-malformed":
		_, err := fmt.Fprint(os.Stdout, `{"actions": [untrusted-sensitive-build-diagnostic`)
		return err
	case "contracts-oversize":
		_, err := fmt.Fprint(os.Stdout, strings.Repeat(" ", (1<<20)+1))
		return err
	}
	definitions := config.Protocol.Actions
	switch config.Mode {
	case "contracts-equivalent":
		slices.Reverse(definitions)
		slices.Reverse(definitions[1].RequiredSecrets)
		// These equivalent spellings must not round the extension's large integer.
		definitions[1].InputSchema = bytes.ReplaceAll(definitions[1].InputSchema, []byte("9007199254740993"), []byte("9007199254740993.0"))
		definitions[1].InputSchema = bytes.ReplaceAll(definitions[1].InputSchema, []byte(`"business-label"`), []byte(`"business-\u006cabel"`))
		definitions[1].OutputSchema = json.RawMessage(`{ "additionalProperties": false, "required": ["count"], "properties": {"count": {"minimum": 0.0, "type": "integer"}}, "type": "object" }`)
	case "contracts-unexpected":
		extra := definitions[1]
		extra.Name = "unexpected-action"
		definitions = append(definitions, extra)
	case "contracts-duplicate":
		definitions = append(definitions, definitions[0])
	case "contracts-description":
		definitions[0].Description = "A different description"
	case "contracts-input-schema":
		definitions[0].InputSchema = json.RawMessage(`{"type":"object"}`)
	case "contracts-output-schema":
		definitions[0].OutputSchema = json.RawMessage(`{"type":"object"}`)
	case "contracts-number-precision":
		definitions[0].InputSchema = bytes.ReplaceAll(definitions[0].InputSchema, []byte("9007199254740993"), []byte("9007199254740992"))
	case "contracts-secret":
		definitions[0].RequiredSecrets = []string{"DIFFERENT_TOKEN"}
	case "contracts-duplicate-secret":
		definitions[0].RequiredSecrets = []string{"SERVICE_TOKEN", "SERVICE_TOKEN", "SERVICE_ACCOUNT"}
	}
	value := map[string]any{"agent_type": config.Protocol.AgentType, "actions": definitions}
	if config.Mode == "contracts-wrong-agent" {
		value["agent_type"] = "another-agent"
	}
	if config.Mode == "contracts-unknown-field" {
		value["untrusted-sensitive-build-diagnostic"] = config.Protocol.Secret
	}
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		return err
	}
	if config.Mode == "contracts-trailing" {
		_, err := fmt.Fprintln(os.Stdout, `{"untrusted-sensitive-build-diagnostic":true}`)
		return err
	}
	return nil
}

func validateBuildPrompt(prompt []byte, config BuildConfig) error {
	text := string(prompt)
	if !strings.Contains(text, "Requested agent:\n"+config.Protocol.Instructions+"\n\nRequested action contracts (JSON):\n") || strings.Contains(text, config.Protocol.Secret) {
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
	expected := config.Protocol.Actions
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

func writeRelease(root string, config BuildConfig) error {
	agentType := config.Protocol.AgentType
	if config.Mode == "template-identity" {
		agentType = "example-agent"
	}
	files := map[string]string{
		"agent.go":                   "package generated\nconst AgentType = " + fmt.Sprintf("%q", agentType) + "\n",
		"LICENSE":                    "MIT License\n",
		"AGENTS.md":                  "Keep action implementations in actions and tests in tests.\n",
		"docker/Dockerfile":          "FROM scratch\n",
		".github/scripts/release.go": "package main\nfunc main() {}\n",
	}
	for _, name := range []string{"make-release", "test", "build-binaries", "push", "push-stable"} {
		files[".github/workflows/"+name+".yml"] = "name: release fixture\non: workflow_dispatch\njobs: {}\n"
	}
	files[".github/workflows/make-release.yml"] = config.Protocol.ReleaseWorkflow
	if config.Mode == "unguarded-release" {
		files[".github/workflows/make-release.yml"] = strings.Replace(config.Protocol.ReleaseWorkflow, `test "$GITHUB_SHA" = "$EXPECTED_COMMIT"`, "true", 1)
	}
	if config.Mode == "local-replace" {
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

func runRelease(args []string, config BuildConfig) error {
	if len(args) < 5 || args[1] != "--version" || args[2] != "v0.1.0" || args[3] != "--repository" || args[4] != "release-check/generated-agent" {
		return errors.New("release helper was not checked with the reserved dry-run identity")
	}
	image := "ghcr.io/release-check/" + config.Protocol.Binary
	if args[0] == "metadata" && len(args) == 5 {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"version": "v0.1.0", "type": config.Protocol.AgentType, "binary": config.Protocol.Binary, "image": image})
	}
	if args[0] != "manifest" || len(args) != 9 || args[5] != "--digest" || args[6] != "sha256:"+strings.Repeat("0", 64) || args[7] != "--assets" {
		return errors.New("unexpected manifest verification command")
	}
	assets := args[8]
	if relative, err := filepath.Rel(config.Source, assets); err != nil || filepath.IsLocal(relative) || !filepath.IsAbs(assets) {
		return errors.New("dry-run release assets must stay outside source")
	}
	value := manifest.Manifest{SchemaVersion: 1, Type: config.Protocol.AgentType, Version: "v0.1.0", Native: map[string]manifest.NativeAsset{}, Docker: &manifest.Docker{Image: image + "@" + args[6]}}
	if config.Mode != "missing-package-descriptions" {
		value.Responsibilities = []string{"Count words in supplied text"}
		for _, definition := range config.Protocol.Actions {
			value.Actions = append(value.Actions, manifest.Action{Name: definition.Name, Description: definition.Description})
		}
		if config.Mode == "stale-manifest-actions" {
			value.Actions[0].Description = "Stale action description"
		}
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			asset := config.Protocol.Binary + "_" + goos + "_" + arch
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
