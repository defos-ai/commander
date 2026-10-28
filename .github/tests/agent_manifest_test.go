package release_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	fixtures "github.com/defos-ai/commander/.github/tests/fixtures"
	"go.yaml.in/yaml/v3"
)

func TestCIAgentManifestGeneration(t *testing.T) {
	generator := fixtures.BuildAgentManifestGenerator(t)
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, name := range []string{"builder", "spawner"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for _, asset := range fixtures.AgentNativeBinaryNames(name) {
				if err := os.WriteFile(filepath.Join(dir, asset), []byte(asset), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(dir, "agent.yaml")
			args := []string{"--agent", name, "--version", "v0.2.0", "--digest", digest, "--output", path, "--artifacts", dir}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if output, err := exec.CommandContext(ctx, generator, args...).CombinedOutput(); err != nil {
				t.Fatalf("generate: %v: %s", err, output)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var definition struct {
				SchemaVersion    int `yaml:"schema_version"`
				Type, Version    string
				Responsibilities []string
				Actions          []struct{ Name, Description string }
				Docker           struct{ Image string }
				Native           map[string]struct{ Asset, SHA256 string }
			}
			if err := yaml.Unmarshal(data, &definition); err != nil {
				t.Fatal(err)
			}
			if definition.SchemaVersion != 1 || definition.Type != name+"-agent" || definition.Version != "v0.2.0" {
				t.Fatalf("wrong identity: %s", data)
			}
			if len(definition.Responsibilities) == 0 {
				t.Fatal("manifest omitted agent responsibilities")
			}
			for _, responsibility := range definition.Responsibilities {
				if strings.TrimSpace(responsibility) == "" {
					t.Error("manifest contains a blank responsibility")
				}
			}
			wantActions := map[string]bool{"spawn-agent": true}
			if name == "builder" {
				wantActions = map[string]bool{"checkout-template": true, "build-agent": true, "publish-agent": true, "release-agent": true}
			}
			if len(definition.Actions) != len(wantActions) {
				t.Fatalf("manifest action count = %d, want %d", len(definition.Actions), len(wantActions))
			}
			for _, action := range definition.Actions {
				if !wantActions[action.Name] || strings.TrimSpace(action.Description) == "" {
					t.Errorf("manifest action lacks its registered name and description: %+v", action)
				}
				delete(wantActions, action.Name)
			}
			if want := "ghcr.io/defos-ai/commander-" + name + "-agent@" + digest; definition.Docker.Image != want {
				t.Fatalf("image = %q, want %q", definition.Docker.Image, want)
			}
			if len(definition.Native) != 6 {
				t.Fatalf("expected six native platforms, got %d", len(definition.Native))
			}
			for _, asset := range fixtures.AgentNativeBinaryNames(name) {
				platform := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(asset, "commander-"+name+"-agent_"), ".exe"), "_", "/")
				got := definition.Native[platform]
				if got.Asset != asset || got.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(asset))) {
					t.Errorf("%s does not reference and hash the release bytes: %+v", platform, got)
				}
			}
		})
	}

	for _, mode := range []string{"unknown agent", "invalid version", "invalid digest", "no artifacts", "missing", "empty", "directory", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			agent, version, imageDigest := "spawner", "v0.2.0", digest
			for _, asset := range fixtures.AgentNativeBinaryNames("spawner") {
				if err := os.WriteFile(filepath.Join(dir, asset), []byte(asset), 0600); err != nil {
					t.Fatal(err)
				}
			}
			asset := filepath.Join(dir, "commander-spawner-agent_linux_amd64")
			if mode == "missing" || mode == "empty" || mode == "directory" || mode == "symlink" {
				if err := os.Remove(asset); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch mode {
			case "unknown agent":
				agent = "web"
			case "invalid version":
				version = "apps/agents/spawner/v0.2.0"
			case "invalid digest":
				imageDigest = "stable"
			case "empty":
				err = os.WriteFile(asset, nil, 0600)
			case "directory":
				err = os.Mkdir(asset, 0700)
			case "symlink":
				err = os.Symlink(filepath.Join(dir, "commander-spawner-agent_linux_arm64"), asset)
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "agent.yaml")
			args := []string{"--agent", agent, "--version", version, "--digest", imageDigest, "--output", path}
			if mode != "no artifacts" {
				args = append(args, "--artifacts", dir)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if output, err := exec.CommandContext(ctx, generator, args...).CombinedOutput(); err == nil {
				t.Fatalf("invalid release input accepted: %s", output)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("failed generation must not leave a publishable manifest")
			}
		})
	}
}
